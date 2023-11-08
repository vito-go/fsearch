package fsearch

import (
	"encoding/json"
	"fmt"
	"github.com/vito-go/fsearch/util"
	"golang.org/x/net/websocket"
	"log"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	dir      string
	appName  string
	hostName string
	dirGrep  *DirGrep
}

// NewClient Create a new client. dir is the directory to be searched. appName is the name of the application.
// hostName is the name of the host where the application is located,
// which is used to distinguish the host where the file is located. it can be empty.
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
	//search, err := fsearch.NewDirSearch(dir)
	//if err != nil {
	//	return nil, err
	//}
	return &Client{
		dir:      dir,
		hostName: hostName,
		appName:  appName,
		dirGrep:  &DirGrep{Dir: dir},
	}, nil
}

// RegisterToCenter register to center. wsAddr is the address of the center.
func (c *Client) RegisterToCenter(wsAddr string) {
	go c.register(wsAddr, c.appName, c.hostName)
}

func (c *Client) RegisterWithHTTP(port uint16, searchPath string) {
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc(searchPath, c.searchText)
		addr := fmt.Sprintf(":%d", port)
		log.Println("unilog Client: ready to start http server, addr: []", addr)
		err := http.ListenAndServe(addr, mux)
		if err != nil {
			log.Println("ListenAndServe error:", err.Error())
		}
	}()

}

// register route address. if register success, it will check files every 30 seconds.
// if files changed, it will send the changed files to the center.
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
	b := NewSchemeBytes(appName, hostName)
	err = websocket.Message.Send(ws, b[:])
	if err != nil {
		log.Println("send error:", err.Error())
		return
	}
	go func() {
		var oldFiles []string
		for {
			newFiles := c.dirGrep.FileNames()
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
		filesMap := make(map[string]struct{}, len(param.Files))
		for _, file := range param.Files {
			filesMap[file] = struct{}{}
		}
		var builder strings.Builder
		c.dirGrep.SearchAndWrite(&SearchAndWriteParam{
			Writer:   &builder,
			HostName: hostName,
			MaxLines: maxLines,
			FileMap:  filesMap,
			Kws:      param.Kws,
		})
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
	// every 30 seconds, send files to center if files changed.
}

type searchParam struct {
	RequestId   int64       `json:"requestId"`
	ContentType ContentType `json:"contentType"`
	MaxLines    int         `json:"maxLines"`
	Files       []string    `json:"files"`
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
	// CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Max-Age", strconv.FormatInt(int64(time.Second*60*60*24*3), 10))
		return
	}
	query := r.URL.Query()
	kws := query["kw"]
	files := query["files"]
	maxLines, err := strconv.Atoi(query.Get("maxLines"))
	if err != nil {
		maxLines = defaultMaxLines
	}
	if maxLines <= 0 {
		maxLines = defaultMaxLines
	}
	if maxLines > maxLinesLimit {
		maxLines = maxLinesLimit
	}
	filesMap := make(map[string]struct{}, len(files))
	for _, file := range files {
		filesMap[file] = struct{}{}
	}
	c.dirGrep.SearchAndWrite(&SearchAndWriteParam{
		Writer:   w,
		HostName: c.hostName,
		MaxLines: maxLines,
		FileMap:  filesMap,
		Kws:      kws,
	})
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Strings(a)
	sort.Strings(b)
	return reflect.DeepEqual(a, b)
}

const defaultMaxLines = 64
const maxLinesLimit = 100
