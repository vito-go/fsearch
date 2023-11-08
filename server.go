package fsearch

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/websocket"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Server struct {
	searchPathHTTP      string
	indexPath           string
	configPath          string
	wsRegisterPath      string
	appHostData         appHostData
	searchResultSyncMap *searchResultSyncMap
	wsSyncMap           *wsSyncMap
}

// NewServer create a new unilog server. searchPath is the search path. indexPath is the static file path.
// configPath is calculated based on searchPath. wsRegisterPath is the websocket register path.
// for example:
//
// searchPath: /search
//
// indexPath: /index/
//
// configPath: /user/_internal/config
//
// wsRegisterPath: /ws
func NewServer(searchPath string, indexPath string, registerPath string) *Server {
	searchPathHttp := searchPath
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
		wsRegisterPath:      registerPath,
		wsSyncMap:           &wsSyncMap{mux: sync.RWMutex{}, dataMap: make(map[uint64]*websocket.Conn)},
		configPath:          configPath,
		searchResultSyncMap: &searchResultSyncMap{mux: sync.RWMutex{}, dataMap: make(map[int64]chan *sendData, 1024)},
		appHostData:         appHostData{mux: sync.RWMutex{}, data: make(map[string]map[uint64]*NodeInfo)},
	}
}

// RegisterWithMux register to mux. fileSystem is the static file system.
func (s *Server) RegisterWithMux(mux *http.ServeMux, fileSystem http.FileSystem) {
	mux.HandleFunc(s.searchPathHTTP, s.searchTextHTTP)
	mux.HandleFunc(s.configPath, s.configHandler)
	mux.Handle(s.indexPath, http.FileServer(fileSystem))
	mux.Handle(s.wsRegisterPath, websocket.Handler(s.registerWS))
}

// RegisterWithGin register to gin. fileSystem is the static file system.
func (s *Server) RegisterWithGin(mux *gin.Engine, fileSystem http.FileSystem) {
	mux.GET(s.searchPathHTTP, func(ctx *gin.Context) {
		s.searchTextHTTP(ctx.Writer, ctx.Request)
	})

	mux.GET(s.configPath, func(ctx *gin.Context) {
		s.configHandler(ctx.Writer, ctx.Request)
	})

	mux.StaticFS(s.indexPath, fileSystem)
	mux.GET(s.wsRegisterPath, func(ctx *gin.Context) {
		websocket.Handler(s.registerWS).ServeHTTP(ctx.Writer, ctx.Request)
	})
}

// StartListenAndServe start server. addr is the listen address, for example: :9097
// fileSystem is the static file system.
func (s *Server) StartListenAndServe(fileSystem http.FileSystem, addr string) error {
	mux := http.ServeMux{}
	mux.HandleFunc(s.searchPathHTTP, s.searchTextHTTP)
	mux.HandleFunc(s.configPath, s.configHandler)
	mux.Handle(s.indexPath, http.FileServer(fileSystem))
	mux.Handle(s.wsRegisterPath, websocket.Handler(s.registerWS))
	return http.ListenAndServe(addr, &mux)
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
	var buf schemeBytes
	copy(buf[:], bufBytes)
	log.Println(ws.Request().RemoteAddr, "register", buf.String())
	if buf.Scheme() != protocol {
		log.Printf("unilog: client protocol check error. it's should be `%s` ,but it's `%s`\n", protocol, buf.Scheme())
		return
	}
	if !buf.CheckReserved() {
		log.Printf("unilog: client protocol error. it's should be `00` ,but it's `%d`\n", buf[6:8])
		return
	}
	appName := buf.AppName()
	// please attention: ws.Request().RemoteAddr is not the real remote addr.
	// todo: and here is not support ipv6 and not support proxy
	nodeId := atomic.AddUint64(&nodeIdGen, 1)
	if s.appHostData.ExistsBy(appName, nodeId) {
		log.Println("unilog : client already exists")
		return
	}
	hostName := buf.HostName()
	s.wsSyncMap.Set(nodeId, ws)
	defer s.appHostData.Del(appName, nodeId)
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
			s.appHostData.Set(appName, nodeId, hostName, files)
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
}

func (s *Server) configHandler(w http.ResponseWriter, r *http.Request) {
	// to avoid CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Max-Age", strconv.FormatInt(int64(time.Second*60*60*24*3), 10))
		return
	}
	body := respBody{
		Code:    0,
		Message: "",
		Data: webConfigData{
			ClusterNodes:   s.appHostData.GetClusterNodes(),
			SearchPathHTTP: s.searchPathHTTP,
		},
	}
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.Write(body.ToBytes())
}

func (s *Server) searchTextHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Max-Age", strconv.FormatInt(int64(time.Second*60*60*24*3), 10))
		return
	}
	query := r.URL.Query()
	appName := query.Get("appName")
	if appName == "" {
		if w, ok := w.(http.ResponseWriter); ok {
			w.WriteHeader(http.StatusBadRequest)
		}
		w.Write([]byte("no appName"))
		return
	}
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
			w.Write([]byte(fmt.Sprintf("nodeId error: %s", err.Error())))
			return
		}
		nodeId = uint64(uniqueIdInt)
	}

	kws := query["kw"]
	files := query["files"]
	hostName := query.Get("hostName")
	log.Printf("remote addr: %s, query is=> %+v\n", r.RemoteAddr, query)
	maxLines, err := strconv.ParseInt(query.Get("maxLines"), 10, 64)
	if err != nil {
		maxLines = defaultMaxLines
	}
	if maxLines > maxLinesLimit {
		maxLines = maxLinesLimit
	}
	s.write(w, appName, hostName, nodeId, int(maxLines), files, kws...)
}
func (s *Server) write(w http.ResponseWriter, appName, hostName string, nodeId uint64, maxLines int, files []string, kws ...string) {
	if len(kws) == 0 {
		return
	}
	var conns []*websocket.Conn
	nodeInfos := s.appHostData.GetHostNodeInfoMap(appName)
	if len(nodeInfos) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(fmt.Sprintf("error: no HostName, appName: %s", appName)))
		return
	}
	if nodeId > 0 {
		if node, ok := nodeInfos[nodeId]; ok {
			if conn, ok := s.wsSyncMap.Get(node.NodeId); ok {
				conns = append(conns, conn)
			} else {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(fmt.Sprintf("error: cluster node not found. please refresh the page")))
				return
			}
		} else {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(fmt.Sprintf("error: cluster node not found. please refresh the page")))
			return
		}
	} else if hostName != "" {
		infoMap := s.appHostData.GetHostNodeInfoMap(appName)
		for _, info := range infoMap {
			if info.HostName == hostName {
				if conn, ok := s.wsSyncMap.Get(info.NodeId); ok {
					conns = append(conns, conn)
					break
				}
			}
		}
	} else {
		ids := s.appHostData.GetHostNodeUniqueIds(appName)
		for _, uid := range ids {
			if conn, ok := s.wsSyncMap.Get(uid); ok {
				conns = append(conns, conn)
			}
		}
	}
	if len(conns) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(fmt.Sprintf("error: no node, appName: %s", appName)))
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
			case <-time.After(time.Second * 6):
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
			flusher.Flush()
		}
	}
}

type respBody struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

func (r *respBody) ToBytes() []byte {
	b, _ := json.Marshal(r)
	return b
}
