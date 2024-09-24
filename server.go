package fsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/websocket"
	"log"
	"net"
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
	privateIpPath       string
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

var accountConfigNoAuth = &AccountConfig{Username: "_", Password: "_", AllowedAppNames: nil, ExcludedAppNames: nil}

const (
	WsRegisterSubPath    = "_ws"
	PrivateIpPathSubPath = "_internal/privateIp"
)
const (
	searchPathSubPath = "_search"
	configPathSubPath = "_internal/config"
)

// NewServer create a new fsearch server. searchPath is the search path. indexPath is the static file path, it must end with /.
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
func NewServer(indexPath string, authMap map[string]*AccountConfig) *Server {
	if !strings.HasSuffix(indexPath, "/") {
		panic("indexPath must end with /")
	}
	searchPath := indexPath + searchPathSubPath
	wsRegisterPath := indexPath + WsRegisterSubPath
	var configPath string
	var privateIpPath string
	configPath = indexPath + configPathSubPath
	privateIpPath = indexPath + PrivateIpPathSubPath
	return &Server{
		searchPathHTTP:      searchPath,
		indexPath:           indexPath,
		wsRegisterPath:      wsRegisterPath,
		wsSyncMap:           &wsSyncMap{mux: sync.RWMutex{}, dataMap: make(map[uint64]*websocket.Conn)},
		configPath:          configPath,
		privateIpPath:       privateIpPath,
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
	g := ginHandler{s: s, fs: fileSystem}
	mux.HEAD(s.indexPath+"*path", g.Handle)
	mux.GET(s.indexPath+"*path", g.Handle)
}

type ginHandler struct {
	s  *Server
	fs http.FileSystem
}

func (g *ginHandler) Handle(ctx *gin.Context) {
	prefix := strings.TrimSuffix(g.s.indexPath, "/")
	path := ctx.Request.URL.Path
	f := &trimPrefixFileSystem{
		fs:     g.fs,
		prefix: prefix,
	}
	switch path {
	case g.s.searchPathHTTP:
		g.s.searchTextHTTP(ctx.Writer, ctx.Request)
	case g.s.configPath:
		g.s.configHandler(ctx.Writer, ctx.Request)
	case g.s.wsRegisterPath:
		subproto := websocket.Server{
			Handshake: subProtocolHandshake,
			Handler:   g.s.registerWS,
		}
		subproto.ServeHTTP(ctx.Writer, ctx.Request)
	case g.s.privateIpPath:
		g.s.privateIp(ctx.Writer, ctx.Request)
	default:
		_, pass := g.s.checkAuth(ctx.Writer, ctx.Request)
		if !pass {
			return
		}
		http.FileServer(f).ServeHTTP(ctx.Writer, ctx.Request)
	}
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

type RegisterInfo struct {
	AppName  string `json:"appName"`
	HostName string `json:"hostName"`
}

const wsProtocol = "lsh"

func (s *Server) registerWS(ws *websocket.Conn) {
	defer ws.Close()
	var bufBytes []byte
	err := websocket.Message.Receive(ws, &bufBytes)
	if err != nil {
		return
	}
	var registerInfo RegisterInfo
	err = json.Unmarshal(bufBytes, &registerInfo)
	if err != nil {
		return
	}
	appName := registerInfo.AppName
	nodeId := atomic.AddUint64(&nodeIdGen, 1)
	hostName := registerInfo.HostName
	s.wsSyncMap.Set(nodeId, ws)
	s.appHostData.Set(appName, nodeId, hostName, nil)
	defer s.appHostData.Del(appName, nodeId)
	defer s.wsSyncMap.Del(nodeId)
	for {
		var sendBytes []byte
		err = websocket.Message.Receive(ws, &sendBytes) // block until the client close the connection
		if err != nil {
			return
		}
		var data sendData
		err = json.Unmarshal(sendBytes, &data)
		if err != nil {
			return
		}
		switch data.ContentType {
		case contentTypeFiles:
			var files []string
			err = json.Unmarshal([]byte(data.Content), &files)
			if err != nil {
				return
			}
			sort.Strings(files)
			log.Printf("client regiter: appName: %s, NodeId:%d HostName: %s Files: %v", appName, nodeId, hostName, files)
			s.appHostData.Set(appName, nodeId, hostName, files)
		case contentTypeSearchResult:
			dataCh, ok := s.searchResultSyncMap.Get(data.RequestId)
			if !ok {
				log.Printf("fsearch: unknown requestId: %d", data.RequestId)
				continue
			}
			select {
			case dataCh <- &data:
			default:
				log.Printf("fsearch: dataCh is full, drop data, requestId: %d", data.RequestId)
			}
		default:
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
		return accountConfigNoAuth, true
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
			return accountConfigNoAuth, true
		}
	}
	user, password, ok := r.BasicAuth()
	if !ok {
		return nil, false
	}
	expected, ok := s.authMap[user]
	if !ok {
		return nil, false
	}
	if expected.Password != password {
		return nil, false
	}
	return expected, true
}
func (s *Server) privateIp(w http.ResponseWriter, r *http.Request) {
	if cors(w, r) {
		return
	}
	//_, ok := s.checkAuth(w, r)
	//if !ok {
	//	return
	//}
	privateIPs, err := getPrivateIPs()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	b, _ := json.Marshal(privateIPs)
	w.Write(b)
}
func (s *Server) configHandler(w http.ResponseWriter, r *http.Request) {
	// to avoid CORS
	if cors(w, r) {
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
func cors(w http.ResponseWriter, r *http.Request) (aborted bool) {
	origin := r.Header.Get("origin")
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "OPTIONS,POST,GET")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}
func (s *Server) searchTextHTTP(w http.ResponseWriter, r *http.Request) {
	if cors(w, r) {
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
	maxLines, err := strconv.ParseInt(query.Get("maxLines"), 10, 64)
	if err != nil {
		maxLines = defaultMaxLines
	}
	if maxLines > maxLinesLimit {
		maxLines = maxLinesLimit
	}
	dataType := query.Get("dataType") // support text, html
	fontSize, _ := strconv.ParseInt(query.Get("fontSize"), 10, 64)
	normalColor := query.Get("normalColor")
	isHref, _ := strconv.ParseBool(query.Get("isHref"))
	hrefQuery := r.URL.Query()
	hrefQuery.Set("isHref", "true")

	if fontSize > 0 {
		hrefQuery.Set("fontSize", strconv.FormatInt(fontSize, 10))
	}
	if normalColor != "" {
		hrefQuery.Set("normalColor", normalColor)
	}
	hrefQuery.Set("kw", replaceTraceId)
	// todo
	locationOrigin := query.Get("locationOrigin")
	if locationOrigin == "" {
		locationOrigin = r.Header.Get("Origin")
	}
	if locationOrigin == "" {
		locationOrigin = r.Header.Get("Referer")
	}
	traceIdHref := fmt.Sprintf("%s%s?%s", locationOrigin, s.searchPathHTTP, hrefQuery.Encode())
	s.write(r.Context(), isHref, fontSize, normalColor, dataType, traceIdHref, w, appName, hostName, nodeId, int(maxLines), files, kws...)
}

func (s *Server) write(ctx context.Context, isHref bool, fontSize int64, normalColor string, dataType, traceIdHref string, w http.ResponseWriter, appName, hostName string, nodeId uint64, maxLines int, files []string, kws ...string) {
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
				log.Printf("fsearch: ws remote addr: %s: send error: %s\n", c.Request().RemoteAddr, err.Error())
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
		if dataType == "html" {
			if err := WriteColorHTML(isHref, normalColor, fontSize, traceIdHref, strings.NewReader(data.Content), w); err != nil {
				return
			}
		} else {
			// text
			if _, err := w.Write([]byte(data.Content)); err != nil {
				return
			}
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

type trimPrefixFileSystem struct {
	fs     http.FileSystem
	prefix string
}

func (t *trimPrefixFileSystem) Open(name string) (http.File, error) {
	name = strings.TrimPrefix(name, t.prefix)
	if name == "" {
		name = "/"
	}
	return t.fs.Open(name)
}
func subProtocolHandshake(config *websocket.Config, req *http.Request) error {
	for _, proto := range config.Protocol {
		if proto == wsProtocol {
			config.Protocol = []string{proto}
			return nil
		}
	}
	return websocket.ErrBadWebSocketProtocol
}

func (s *Server) registerWithMux(mux *http.ServeMux, fileSystem http.FileSystem) {
	mux.HandleFunc(s.searchPathHTTP, s.searchTextHTTP)
	mux.HandleFunc(s.configPath, s.configHandler)
	mux.HandleFunc(s.privateIpPath, s.privateIp)
	fs := http.FileServer(&trimPrefixFileSystem{
		fs: fileSystem,
		// prefix not include suffix /
		prefix: strings.TrimSuffix(s.indexPath, "/"),
	})
	mux.HandleFunc(s.indexPath, func(w http.ResponseWriter, r *http.Request) {
		// check auth
		if _, ok := s.checkAuth(w, r); !ok {
			return
		}
		fs.ServeHTTP(w, r)
	})
	subproto := websocket.Server{
		Handshake: subProtocolHandshake,
		Handler:   s.registerWS,
	}
	mux.Handle(s.wsRegisterPath, subproto)
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
func getPrivateIPs() ([]string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var items []string
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.IsPrivate() {
			items = append(items, ipNet.IP.String())
		}
	}
	return items, nil
}
