package unilog

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/websocket"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Server struct {
	searchPathHTTP string
	searchPathWS   string
	indexPath      string
	configPath     string
	wsRegisterPath string

	appHostGlobal       appHost
	searchResultSyncMap *searchResultSyncMap
	wsSyncMap           *wsSyncMap
}
type resultChannel struct {
	mux             sync.RWMutex
	requestIdUidMap map[int64]int64          // 多个requestId对应一个uid
	dataMap         map[int64]chan *sendData // key: uid
}

func (r *resultChannel) Set(uid int64, requestIds []int64) {
	r.mux.Lock()
	defer r.mux.Unlock()
	ch := make(chan *sendData, 32)
	for _, id := range requestIds {
		r.requestIdUidMap[id] = uid
	}
	r.dataMap[uid] = ch
}

func (r *resultChannel) Del(uid int64, requestIds []int64) {
	r.mux.Lock()
	defer r.mux.Unlock()
	delete(r.dataMap, uid)

	for _, id := range requestIds {
		delete(r.requestIdUidMap, id)
	}
}
func (r *resultChannel) GetByRequestId(reqId int64) (chan *sendData, bool) {
	r.mux.RLock()
	defer r.mux.RUnlock()
	uid, ok := r.requestIdUidMap[reqId]
	if !ok {
		return nil, false
	}
	if ch, ok := r.dataMap[uid]; ok {
		return ch, true
	}
	return nil, false
}
func (r *resultChannel) Get(uid int64) (chan *sendData, bool) {
	r.mux.RLock()
	defer r.mux.RUnlock()
	if ch, ok := r.dataMap[uid]; ok {
		return ch, true
	}
	return nil, false
}

type searchResultSyncMap struct {
	mux     sync.RWMutex
	dataMap map[int64]chan *sendData // key: appName_uniqueId
}

func (s *searchResultSyncMap) Set(requestId int64, ch chan *sendData) {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.dataMap[requestId] = ch
}

func (s *searchResultSyncMap) Del(requestId int64) {
	s.mux.Lock()
	defer s.mux.Unlock()
	delete(s.dataMap, requestId)
}

func (s *searchResultSyncMap) Get(requestId int64) (chan *sendData, bool) {
	s.mux.RLock()
	defer s.mux.RUnlock()
	if ch, ok := s.dataMap[requestId]; ok {
		return ch, true
	}
	return nil, false
}

type wsSyncMap struct {
	mux     sync.RWMutex
	dataMap map[uint64]*websocket.Conn // key: appName_uniqueId
}

func (w *wsSyncMap) Del(uid uint64) {
	w.mux.Lock()
	defer w.mux.Unlock()
	delete(w.dataMap, uid)
}

func (w *wsSyncMap) Set(uid uint64, ws *websocket.Conn) {
	w.mux.Lock()
	defer w.mux.Unlock()
	w.dataMap[uid] = ws
}

func (w *wsSyncMap) Get(uid uint64) (*websocket.Conn, bool) {
	w.mux.RLock()
	defer w.mux.RUnlock()
	if ws, ok := w.dataMap[uid]; ok {
		return ws, true
	}
	return nil, false
}

// NewServer 创建一个新的unilog server
// searchPath: 搜索路径 例如: /search
// indexPath: 静态文件路径 例如: /index/
// configPath: 根据searchPath计算出， 例如: /user/_internal/config
// wsRegisterPath: websocket注册路径 例如: /ws
func NewServer(searchPath string, indexPath string, registerPath string) *Server {
	searchPathHttp := searchPath
	searchPathWs := filepath.Join(searchPath, "ws")
	var configPath string
	if searchPath == "/" {
		configPath = "/_internal/config"
	} else {
		temp := strings.TrimSuffix(searchPath, "/")
		// change /user/home/  to /user/_internal/config
		configPath = temp[:strings.LastIndex(temp, "/")] + "/_internal/config"
	}

	return &Server{searchPathHTTP: searchPathHttp,
		indexPath:           indexPath,
		searchPathWS:        searchPathWs,
		wsRegisterPath:      registerPath,
		wsSyncMap:           &wsSyncMap{mux: sync.RWMutex{}, dataMap: make(map[uint64]*websocket.Conn)},
		configPath:          configPath,
		searchResultSyncMap: &searchResultSyncMap{mux: sync.RWMutex{}, dataMap: make(map[int64]chan *sendData, 1024)},
		appHostGlobal:       appHost{mux: sync.RWMutex{}, appHostMap: make(map[string]map[uint64]*NodeInfo)},
	}
}

func (s *Server) RegisterWithMux(mux *http.ServeMux, fileSystem http.FileSystem) {
	mux.HandleFunc(s.searchPathHTTP, s.searchTextHTTP)
	mux.Handle(s.searchPathWS, websocket.Handler(s.searchTextWS))
	mux.HandleFunc(s.configPath, s.configHandler)
	mux.Handle(s.indexPath, http.FileServer(fileSystem))
	mux.Handle(s.wsRegisterPath, websocket.Handler(s.registerWS))
}

func (s *Server) RegisterWithGin(mux *gin.Engine, fileSystem http.FileSystem) {
	mux.GET(s.searchPathHTTP, func(ctx *gin.Context) {
		s.searchTextHTTP(ctx.Writer, ctx.Request)
	})
	mux.GET(s.searchPathWS, func(ctx *gin.Context) {
		websocket.Handler(s.searchTextWS).ServeHTTP(ctx.Writer, ctx.Request)
	})

	mux.GET(s.configPath, func(ctx *gin.Context) {
		s.configHandler(ctx.Writer, ctx.Request)
	})

	mux.StaticFS(s.indexPath, fileSystem)
	mux.GET(s.wsRegisterPath, func(ctx *gin.Context) {
		websocket.Handler(s.registerWS).ServeHTTP(ctx.Writer, ctx.Request)
	})
}

// StartListenAndServe addr for example :8080
func (s *Server) StartListenAndServe(fileSystem http.FileSystem, addr string) error {
	mux := http.ServeMux{}
	mux.HandleFunc(s.searchPathHTTP, s.searchTextHTTP)
	mux.Handle(s.searchPathWS, websocket.Handler(s.searchTextWS))
	mux.HandleFunc(s.configPath, s.configHandler)
	mux.Handle(s.indexPath, http.FileServer(fileSystem))
	mux.Handle(s.wsRegisterPath, websocket.Handler(s.registerWS))
	return http.ListenAndServe(addr, &mux)
}

var nodeIdGen uint64 = 10000

func (s *Server) registerWS(ws *websocket.Conn) {
	defer ws.Close()
	var bufBytes []byte
	log.Println("ready to accept a new client")
	err := websocket.Message.Receive(ws, &bufBytes)
	if err != nil {
		log.Println("unilog: read error:", err.Error())
		return
	}
	var buf schemaBytes
	copy(buf[:], bufBytes)
	log.Println(ws.Request().RemoteAddr, "register", buf.String())
	if buf.Scheme() != protocol {
		log.Printf("unilog: client protocol check error. it's should be `%s` ,but it's `%s`\n", protocol, buf.Scheme())
		return
	}
	if !buf.Check00() {
		log.Printf("unilog: client protocol error. it's should be `00` ,but it's `%d`\n", buf[6:8])
		return
	}
	appName := buf.AppName()
	// please attention: ws.Request().RemoteAddr is not the real remote addr.
	// todo: and here is not support ipv6 and not support proxy
	//nodeId:=buf.NodeId()
	nodeId := atomic.AddUint64(&nodeIdGen, 1)
	if s.appHostGlobal.ExistsBy(appName, nodeId) {
		log.Println("unilog : client already exists")
		return
	}
	hostName := buf.HostName()
	s.wsSyncMap.Set(nodeId, ws)

	defer s.appHostGlobal.Del(appName, nodeId)
	defer s.wsSyncMap.Del(nodeId)
	for {
		var sendBytes []byte
		err = websocket.Message.Receive(ws, &sendBytes) // 阻塞等待客户端关闭
		if err != nil {
			log.Println("unilog: read error:", err.Error())
			return
		}
		var data sendData
		err = json.Unmarshal(sendBytes, &data)
		if err != nil {
			log.Println("unilog: json.Unmarshal error:", err.Error())
			return
		}
		switch data.ContentType {
		case contentTypeFiles:
			var files []string
			err = json.Unmarshal([]byte(data.Content), &files)
			if err != nil {
				log.Println("unilog: json.Unmarshal error:", err.Error())
				return
			}
			sort.Strings(files)
			log.Printf("client regiter: appName: %s, NodeId:%d HostName: %s Files: %v", appName, nodeId, hostName, files)
			s.appHostGlobal.Set(appName, nodeId, hostName, files)
		case contentTypeSearchResult:
			dataCh, ok := s.searchResultSyncMap.Get(data.RequestId)
			if !ok {
				log.Printf("unilog: unknown requestId: %d", data.RequestId)
				continue
			}
			select {
			case dataCh <- &data:
			default:
				log.Printf("unilog: default unknown requestId: %d", data.RequestId)
			}
		default:
			log.Println("unilog: unknown ContentType:", data.ContentType)
			return
		}

	}
}

type webConfigData struct {
	ClusterNodes   []ClusterNode `json:"clusterNodes,omitempty"`
	SearchPathHTTP string        `json:"searchPathHTTP,omitempty"`
	SearchPathWS   string        `json:"searchPathWS,omitempty"`
	ConfigPath     string        `json:"configPath,omitempty"`
}

func (s *Server) configHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Max-Age", strconv.FormatInt(int64(time.Second*60*60*24*3), 10))
		return
	}
	respBody := RespBody{
		Code:    0,
		Message: "",
		Data: webConfigData{
			ClusterNodes:   s.appHostGlobal.GetClusterNodes(),
			SearchPathHTTP: s.searchPathHTTP,
			SearchPathWS:   s.searchPathWS,
			ConfigPath:     s.configPath,
		},
	}
	w.Write(respBody.ToBytes())
}

func (s *Server) searchTextWS(ws *websocket.Conn) {
	defer ws.Close()
	s.writeWith(ws, ws.Request())
}
func (s *Server) searchTextHTTP(w http.ResponseWriter, r *http.Request) {
	s.writeWith(w, r)
}
func (s *Server) writeWith(w io.Writer, r *http.Request) {
	query := r.URL.Query()
	appName := query.Get("appName")
	var nodeId uint64
	var err error
	if query.Get("nodeId") == "" {
		nodeId = 0
	} else {
		uniqueIdInt, err := strconv.ParseInt(query.Get("nodeId"), 10, 64)
		if err != nil {
			if responseWriter, ok := w.(http.ResponseWriter); ok {
				responseWriter.WriteHeader(http.StatusBadRequest)
			}
			w.Write([]byte("nodeId error"))
			return
		}
		nodeId = uint64(uniqueIdInt)
	}
	//path := query.Get("path")
	if appName == "" {
		if responseWriter, ok := w.(http.ResponseWriter); ok {
			responseWriter.WriteHeader(http.StatusBadRequest)
		}
		w.Write([]byte("no appName"))
		return
	}
	kws := query["kw"]
	files := query["files"]
	hostName := query.Get("hostName")
	log.Println("query is=>", query)
	maxLines, err := strconv.ParseInt(query.Get("maxLines"), 10, 64)
	if err != nil {
		maxLines = defaultMaxLines
	}
	if maxLines > maxLinesLimit {
		maxLines = maxLinesLimit
	}
	s.write(w, appName, hostName, nodeId, int(maxLines), files, kws...)
}
func (s *Server) write(w io.Writer, appName, hostName string, nodeId uint64, maxLines int, files []string, kws ...string) {
	if len(kws) == 0 {
		return
	}
	var conns []*websocket.Conn
	nodeInfos := s.appHostGlobal.GetHostNodeInfoMap(appName)
	if len(nodeInfos) == 0 {
		if responseWriter, ok := w.(http.ResponseWriter); ok {
			responseWriter.WriteHeader(http.StatusBadRequest)
		}
		w.Write([]byte("no HostName"))
		return
	}
	if nodeId > 0 {
		if node, ok := nodeInfos[nodeId]; ok {
			if conn, ok := s.wsSyncMap.Get(node.NodeId); ok {
				conns = append(conns, conn)
			} else {
				if responseWriter, ok := w.(http.ResponseWriter); ok {
					responseWriter.WriteHeader(http.StatusBadRequest)
				}
				w.Write([]byte(fmt.Sprintf("node not found by nodeId: %d", node.NodeId)))
				return
			}
		}
	} else if hostName != "" {
		infoMap := s.appHostGlobal.GetHostNodeInfoMap(appName)
		for _, info := range infoMap {
			if info.HostName == hostName {
				if conn, ok := s.wsSyncMap.Get(info.NodeId); ok {
					conns = append(conns, conn)
					break
				}
			}
		}
	} else {
		uids := s.appHostGlobal.GetHostNodeUniqueIds(appName)
		for _, uid := range uids {
			if conn, ok := s.wsSyncMap.Get(uid); ok {
				conns = append(conns, conn)
			}
		}
	}
	if len(conns) == 0 {
		if responseWriter, ok := w.(http.ResponseWriter); ok {
			responseWriter.WriteHeader(http.StatusBadRequest)
		}
		w.Write([]byte("no node"))
		return
	}
	cli := http.Client{}
	defer cli.CloseIdleConnections()
	var wg sync.WaitGroup
	var requestIds []int64
	for i := 0; i < len(conns); i++ {
		requestId := time.Now().UnixNano()
		requestIds = append(requestIds, requestId)
		s.searchResultSyncMap.Set(requestId, make(chan *sendData, 1))
	}
	for i, conn := range conns {
		reqId := requestIds[i]
		wg.Add(1)
		go func(c *websocket.Conn) {
			defer wg.Done()
			param := searchParam{
				RequestId:   reqId,
				MaxLines:    maxLines,
				Files:       files,
				ContentType: contentTypeSearchParam,
				Kws:         kws,
			}
			err := websocket.Message.Send(c, param.ToBytes())
			if err != nil {
				log.Printf("unilog: ws remote addr: %s: send error: %s\n", c.Request().RemoteAddr, err.Error())
				return
			}
		}(conn)
	}

	defer func() {
		for _, id := range requestIds {
			s.searchResultSyncMap.Del(id)
		}
	}()
	wg.Wait()
	channelData := make(chan *sendData, len(requestIds))

	for _, id := range requestIds {
		ch, ok := s.searchResultSyncMap.Get(id)
		if !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case data := <-ch:
				channelData <- data
			case <-time.After(time.Second * 3):
			}
		}()
	}
	go func() {
		wg.Wait()
		close(channelData)
	}()
	for data := range channelData {
		if data.Content == "" {
			continue
		}
		if _, err := w.Write([]byte(data.Content)); err != nil {
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			log.Println("flush")
			flusher.Flush()
		}
	}
}
