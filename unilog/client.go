package unilog

import (
	"encoding/json"
	"fmt"
	"github.com/vito-go/fsearch"
	"github.com/vito-go/fsearch/util"
	"golang.org/x/net/websocket"
	"log"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	dir      string
	appName  string
	hostName string
	once     sync.Once
	//
	search *fsearch.DirSearch
}

func NewClient(dir string, appName string, hostName string) (*Client, error) {
	if len(appName) == 0 {
		panic("appName can not be empty")
	}
	if hostName == "" {
		hostName, _ = util.GetPrivateIP()
	}
	if hostName == "" {
		hostName = "127.0.0.1"
	}
	search, err := fsearch.NewDirSearch(dir)
	if err != nil {
		return nil, err
	}
	return &Client{
		dir:      dir,
		hostName: hostName,
		appName:  appName,
		search:   search,
		once:     sync.Once{},
	}, nil
}

// RegisterToCenter 用于注册服务，注册成功后，会每隔30秒检查一次files是否有变化，如果有变化，会重新注册 注册路由地址
// export for register
func (c *Client) RegisterToCenter(wsAddr string) {
	go c.register(wsAddr, c.appName, c.hostName)
}

func (c *Client) RegisterWithHTTP(port uint16, searchPath string) {
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc(searchPath, c.searchText)
		addr := fmt.Sprintf(":%d", port)
		log.Println("unilog Client: ready to start http server:", addr)
		err := http.ListenAndServe(addr, mux)
		if err != nil {
			log.Println("ListenAndServe error:", err.Error())
		}
	}()

}

// forRegister 用于注册服务，注册成功后，会每隔30秒检查一次files是否有变化，如果有变化，会重新注册 注册路由地址
func (c *Client) register(addr string, appName string, hostName string) {
	for {
		c.forWS(addr, appName, hostName)
		time.Sleep(time.Second * 10)
	}

}
func (c *Client) forWS(addr string, appName string, hostName string) {
	localHost, _ := util.GetPrivateIP()
	if localHost == "" {
		localHost = "127.0.0.1"
	}
	log.Println("unilog Client: ready to register ws:", addr)
	ws, err := websocket.Dial(addr, "", fmt.Sprintf("http://%s", localHost))
	if err != nil {
		log.Println("register error:", err.Error())
		return
	}
	defer ws.Close()
	b := NewSchemaBytes(appName, hostName)
	err = websocket.Message.Send(ws, b[:])
	if err != nil {
		log.Println("send error:", err.Error())
		return
	}
	go func() {
		var oldFiles []string
		for {
			newFiles := c.search.Files()
			if !slicesEqual(oldFiles, newFiles) {
				log.Printf("ready to send fiels: %+v\n", newFiles)
				newLinesBytes, _ := json.Marshal(newFiles)
				sendBytes, _ := json.Marshal(sendData{ContentType: contentTypeFiles, Content: string(newLinesBytes)})
				err = websocket.Message.Send(ws, sendBytes)
				if err != nil {
					log.Println("send error:", err.Error())
					return
				}
				oldFiles = newFiles
			}
			time.Sleep(time.Second * 30)
		}
	}()

	for {
		var buf []byte
		err = websocket.Message.Receive(ws, &buf)
		if err != nil {
			log.Println("receiver server error: ", err)
			return
		}
		var param searchParam
		err = json.Unmarshal(buf, &param)
		if err != nil {
			log.Printf("json.Unmarshal buf: %s, error: %s\n", string(buf), err.Error())
			return
		}
		maxLines := param.MaxLines
		if maxLines > maxLinesLimit {
			maxLines = maxLinesLimit
		}
		filesMap := make(map[string]bool, len(param.Files))
		for _, file := range param.Files {
			filesMap[file] = true
		}
		var builder strings.Builder
		//lines := c.search.SearchFromEndAndWrite(maxLines, filesMap, param.Kws...)
		c.search.SearchFromEndAndWrite(&builder, hostName, maxLines, filesMap, param.Kws...)
		//var content string
		//if len(lines) > 0 {
		//	content = strings.Join(lines, "\n")
		//}
		result, _ := json.Marshal(sendData{
			RequestId:   param.RequestId,
			Content:     builder.String(),
			ContentType: contentTypeSearchResult,
			AppName:     appName,
			HostName:    hostName,
		})
		err = websocket.Message.Send(ws, result)
		if err != nil {
			return
		}
	}
	// 每30秒检查一次files是否有变化
}

type searchParam struct {
	RequestId   int64       `json:"requestId"`
	ContentType ContentType `json:"contentType"`
	MaxLines    int         `json:"maxLines"`
	Files       []string    `json:"Files"`
	Kws         []string    `json:"kws"`
}

func (p *searchParam) ToBytes() []byte {
	b, _ := json.Marshal(p)
	return b
}

// ContentType 1 Files 2 searchContent 100
// > 100 for server
// < 100 for client
type ContentType int

const contentTypeFiles ContentType = 1
const contentTypeSearchResult ContentType = 2

const contentTypeSearchParam ContentType = 100

type sendData struct {
	RequestId   int64       `json:"requestId"`
	Content     string      `json:"content"`
	ContentType ContentType `json:"ContentType"`
	HostName    string      `json:"hostName"`
	AppName     string      `json:"appName"`
}

func (c *Client) searchText(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Max-Age", strconv.FormatInt(int64(time.Second*60*60*24*3), 10))
		return
	}
	query := r.URL.Query()
	kws := query["kw"]
	files := query["Files"]
	maxLines, err := strconv.Atoi(query.Get("maxLines"))
	if err != nil {
		maxLines = defaultMaxLines
	}
	if maxLines > maxLinesLimit {
		maxLines = maxLinesLimit
	}
	filesMap := make(map[string]bool, len(files))
	for _, file := range files {
		filesMap[file] = true
	}
	c.search.SearchFromEndAndWrite(w, c.hostName, maxLines, filesMap, kws...)
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	// 对切片进行排序
	sort.Strings(a)
	sort.Strings(b)

	// 使用DeepEqual函数比较两个切片是否相等
	return reflect.DeepEqual(a, b)
}

const defaultMaxLines = 50
const maxLinesLimit = 200
