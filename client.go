package fsearch

import (
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/net/websocket"
	"log"
	"net"
	"net/http"
	"os"
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
func NewClient(appName, searchTargetDir string) (*Client, error) {
	if len(appName) == 0 {
		panic("appName can not be empty")
	}
	// hostName is the name of the host where the application is located,
	// which is used to distinguish the host where the file is located.
	hostName, _ := getPrivateIP()
	if hostName == "" {
		hostName, _ = os.Hostname()
	}
	return &Client{
		dir:      searchTargetDir,
		hostName: hostName,
		appName:  appName,
		dirGrep:  &DirGrep{Dir: searchTargetDir},
	}, nil
}

// RegisterToCenter register to center. wsAddr is the address of the center.
// wsAddr is the address of the center, whose format is ws://host:port+<serverIndexPath>+_ws
// for example, serverIndexPath is /, then wsAddr is ws://host:port/_ws
func (c *Client) RegisterToCenter(wsAddr string) {
	go c.register(wsAddr, c.appName, c.hostName)
}

func (c *Client) RegisterWithHTTP(port uint16, searchPath string) error {
	addr := fmt.Sprintf(":%d", port)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc(searchPath, c.searchText)
		err := http.Serve(lis, mux)
		if err != nil {
			log.Printf("http.Serve error: %s\n", err.Error())
		}
	}()
	return nil
}

// register route address. if register success, it will check files every 30 seconds.
// if files changed, it will send the changed files to the center.
func (c *Client) register(addr string, appName string, hostName string) {
	// use time.Ticker to replace time.Sleep
	ticker := time.NewTicker(time.Second * 10)
	defer ticker.Stop()
	for {
		c.forWS(addr, appName, hostName)
		<-ticker.C
	}

}
func (c *Client) forWS(addr string, appName string, hostName string) {
	originHost, _ := getPrivateIP()
	if originHost == "" {
		originHost = "127.0.0.1"
	}
	ws, err := websocket.Dial(addr, wsProtocol, fmt.Sprintf("http://%s", originHost))
	if err != nil {
		log.Println("websocket.Dial error: ", err.Error())
		return
	}
	defer ws.Close()
	registerInfo := RegisterInfo{
		AppName:  appName,
		HostName: hostName,
	}
	b, _ := json.Marshal(registerInfo)
	err = websocket.Message.Send(ws, b[:])
	if err != nil {

		return
	}
	go func() {
		var oldFiles []string
		ticker := time.NewTicker(time.Second * 30)
		defer ticker.Stop()
		for {
			newFiles := c.dirGrep.FileNames()
			if !slicesEqual(oldFiles, newFiles) {
				newLinesBytes, _ := json.Marshal(newFiles)
				sendBytes, _ := json.Marshal(sendData{ContentType: contentTypeFiles, Content: string(newLinesBytes)})
				err = websocket.Message.Send(ws, sendBytes)
				if err != nil {
					return
				}
				oldFiles = newFiles
			}
			<-ticker.C
		}
	}()

	for {
		var buf []byte
		err = websocket.Message.Receive(ws, &buf)
		if err != nil {

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
	if cors(w, r) {
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

const defaultMaxLines = 100
const maxLinesLimit = 256

func getPrivateIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.IsPrivate() {
			return ipNet.IP.String(), err
		}
	}
	return "", errors.New("no private ip")
}
