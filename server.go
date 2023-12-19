package fsearch

import (
	"context"
	"encoding/base64"
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
	// auth map, key: username, value: AccountConfig
	authMap map[string]*AccountConfig
}

// AccountConfig is the account config. when auth is enabled, it will check the username and password.
// if AllowedAppNames is not empty, it will check the appName.
// ExcludedAppNames only exclude the appName in it.
// if AllowedAppNames and ExcludedAppNames are both empty, it will allow all appNames.
// if there is the same one in AllowedAppNames and ExcludedAppNames, it will be excluded.
type AccountConfig struct {
	Username         string
	Password         string
	AllowedAppNames  []string // if empty, all appNames are allowed
	ExcludedAppNames []string // if empty, no appNames are excluded
}

func (a *AccountConfig) CheckAppName(appName string) bool {
	if a.Username == "_" {
		return true
	}
	for _, name := range a.ExcludedAppNames {
		if name == appName {
			return false
		}
	}
	if len(a.AllowedAppNames) > 0 {
		for _, name := range a.AllowedAppNames {
			if name == appName {
				return true
			}
		}
		return false
	}
	return true
}

var AccountConfigNoAuth = &AccountConfig{Username: "_", Password: "_", AllowedAppNames: nil, ExcludedAppNames: nil}

// NewServer create a new unilog server. searchPath is the search path. indexPath is the static file path, it must end with /.
// configPath is calculated based on searchPath. wsRegisterPath is the websocket register path.
// authMap: map[username]AccountConfig, it can be nil if you do not need auth
// for example:
//
// searchPath: /search
//
// indexPath: /
//
// wsRegisterPath: /ws
//
// authMap: nil
func NewServer(searchPath string, indexPath string, registerPath string, authMap map[string]*AccountConfig) *Server {
	if !strings.HasSuffix(indexPath, "/") {
		panic("indexPath must end with /")
	}
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
		authMap:             authMap,
	}
}

// RegisterWithMux register to mux. fileSystem is the static file system.
func (s *Server) RegisterWithMux(mux *http.ServeMux, fileSystem http.FileSystem) {
	s.registerWithMux(mux, fileSystem)
}

// RegisterWithGin register to gin. fileSystem is the static file system.
func (s *Server) RegisterWithGin(mux *gin.Engine, fileSystem http.FileSystem) {
	// user middleware to check auth
	mux.GET(s.searchPathHTTP, func(ctx *gin.Context) {
		s.searchTextHTTP(ctx.Writer, ctx.Request)
	})
	mux.GET(s.configPath, func(ctx *gin.Context) {
		s.configHandler(ctx.Writer, ctx.Request)
	})
	mux.GET(s.wsRegisterPath, func(ctx *gin.Context) {
		websocket.Handler(s.registerWS).ServeHTTP(ctx.Writer, ctx.Request)
	})
	mux.Use(func(ctx *gin.Context) {
		w, r := ctx.Writer, ctx.Request
		if _, ok := s.checkAuth(w, r); !ok {
			ctx.Abort()
			return
		}
		ctx.Next()
	})
	mux.StaticFS(s.indexPath, fileSystem)
}

// StartListenAndServe start server. addr is the listen address, for example: :9097
// fileSystem is the static file system. fileSystem can be fs.EmbeddedFS, http.Dir, or any other http.FileSystem.
func (s *Server) StartListenAndServe(fileSystem http.FileSystem, addr string) error {
	mux := http.ServeMux{}
	s.registerWithMux(&mux, fileSystem)
	return http.ListenAndServe(addr, &mux)
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

var noAuthSuffix = []string{".json", ".js", ".css", ".ico", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".woff2", ".woff", ".ttf", ".eot", ".map", "yaml"}

func (s *Server) checkAuth(w http.ResponseWriter, r *http.Request) (account *AccountConfig, result bool) {
	if len(s.authMap) == 0 {
		return AccountConfigNoAuth, true
	}
	if r.Header.Get("X-Authorization-Clear") == "true" {
		w.Header().Set("WWW-Authenticate", `Basic realm="fsearch"`)
		w.WriteHeader(http.StatusUnauthorized)
		return nil, false
	}
	defer func() {
		if !result {
			w.Header().Set("WWW-Authenticate", `Basic realm="fsearch"`)
			w.WriteHeader(http.StatusUnauthorized)
		}
	}()
	// there is no need to auth for static resource, such as "/manifest.json"
	for _, suffix := range noAuthSuffix {
		if strings.HasSuffix(r.URL.Path, suffix) {
			return AccountConfigNoAuth, true
		}
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Basic ") {
		return nil, false
	}
	encoded := auth[6:]
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false
	}
	ss := strings.Split(string(decoded), ":")
	if len(ss) != 2 {
		return nil, false
	}

	expected, ok := s.authMap[ss[0]]
	if !ok {
		return nil, false
	}
	if expected.Password != ss[1] {
		return nil, false
	}
	return expected, true
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
	user, ok := s.checkAuth(w, r)
	if !ok {
		return
	}
	clusterNodes := s.appHostData.GetClusterNodes()
	clusterNodeFilter := make([]ClusterNode, 0, len(clusterNodes))
	for i, node := range clusterNodes {
		if user.CheckAppName(node.AppName) {
			clusterNodeFilter = append(clusterNodeFilter, clusterNodes[i])
		}
	}
	body := respBody{
		Code:    0,
		Message: "",
		Data: webConfigData{
			ClusterNodes:   clusterNodeFilter,
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
	accountInfo, ok := s.checkAuth(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	appName := query.Get("appName")
	if appName == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("no appName"))
		return
	}
	if !accountInfo.CheckAppName(appName) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Header().Set("WWW-Authenticate", `Basic realm="fsearch"`)
		w.Write([]byte(fmt.Sprintf("appName: %s is not allowed", appName)))
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
	s.write(r.Context(), w, appName, hostName, nodeId, int(maxLines), files, kws...)
}
func (s *Server) write(ctx context.Context, w http.ResponseWriter, appName, hostName string, nodeId uint64, maxLines int, files []string, kws ...string) {
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
			case <-ctx.Done():
				// client close the connection
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

func (s *Server) registerWithMux(mux *http.ServeMux, fileSystem http.FileSystem) {
	mux.HandleFunc(s.searchPathHTTP, s.searchTextHTTP)
	mux.HandleFunc(s.configPath, s.configHandler)
	fs := http.FileServer(fileSystem)
	mux.HandleFunc(s.indexPath, func(w http.ResponseWriter, r *http.Request) {
		// check auth
		if _, ok := s.checkAuth(w, r); !ok {
			return
		}
		fs.ServeHTTP(w, r)
	})
	mux.Handle(s.wsRegisterPath, websocket.Handler(s.registerWS))
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
