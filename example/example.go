package main

import (
	"encoding/json"
	"github.com/vito-go/fsearch"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

func init() {
	// Change current working directory to the directory of this file
	_, file, _, _ := runtime.Caller(0)
	err := os.Chdir(filepath.Dir(file))
	if err != nil {
		panic(err)
	}
}

type clusterNode struct {
	AppName      string              `json:"appName,omitempty"`
	Hosts        []string            `json:"hosts,omitempty"`
	HostFilesMap map[string][]string `json:"hostFilesMap,omitempty"`
}

func main() {
	search, err := fsearch.NewDirSearch("../testdata/")
	if err != nil {
		panic(err)
	}
	const defaultMaxLines = 50
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "*")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Max-Age", strconv.FormatInt(int64(time.Second*60*60*24*3), 10))
			return
		}
		query := r.URL.Query()
		kws := query["kw"]
		maxLines, err := strconv.Atoi(query.Get("maxLines"))
		if err != nil {
			maxLines = defaultMaxLines
		}
		lines := search.SearchFromEnd(maxLines, kws...)

		for _, line := range lines {
			_, _ = w.Write([]byte(line + "\n"))
		}
	})
	mux.HandleFunc("/clusterNodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "*")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Max-Age", strconv.FormatInt(int64(time.Second*60*60*24*3), 10))
			return
		}
		clusterNodes := []clusterNode{
			{
				AppName: "myapp-chat-api-go",
				Hosts:   []string{"127.0.0.1:8181", "127.0.0.1:8182", "127.0.0.1:8183"},
				HostFilesMap: map[string][]string{
					"127.0.0.1:8181": {"myapp-api-user.log", "myapp-api-user1.log", "myapp-api-user2.log"},
					"127.0.0.1:8182": {"newsapp-chat-api-go-2023-08-25T12-53-45.285.log", "newsapp-chat-api-go-2023-08-25T12-53-45.265.log", "newsapp-chat-api-go-2023-08-25T22-53-45.285.log"},
					"127.0.0.1:8183": {"newsapp-chat-user-go-2023-08-25T12-53-45.285.log", "newsapp-chat-user-go-2023-08-25T12-53-45.265.log", "newsapp-chat-user-go-2023-08-25T22-53-45.285.log"},
				},
			},
			{
				AppName: "myapp-chat-api-go1",
				Hosts:   []string{"10.158.13.6:7181", "10.158.13.6:7185", "10.158.13.6:7187"},
				HostFilesMap: map[string][]string{
					"10.158.13.6:7181": {"myapp-api-user.log", "myapp-api-user1.log", "myapp-api-user2.log"},
					"10.158.13.6:7185": {"newsapp-chat-api-go-2023-08-25T12-53-45.285.log", "newsapp-chat-api-go-2023-08-25T12-53-45.265.log", "newsapp-chat-api-go-2023-08-25T22-53-45.285.log"},
					"10.158.13.6:7187": {"newsapp-chat-user-go-2023-08-25T12-53-45.285.log", "newsapp-chat-user-go-2023-08-25T12-53-45.265.log", "newsapp-chat-user-go-2023-08-25T22-53-45.285.log"},
				},
			},
			{
				AppName: "myapp-chat-api-go3",
				Hosts:   []string{"10.158.13.6:9181", "10.158.13.6:9185", "10.158.13.6:9187"},
				HostFilesMap: map[string][]string{
					"10.158.13.6:9181": {"myapp-api-user.log", "myapp-api-user1.log", "myapp-api-user2.log"},
					"10.158.13.6:9185": {"newsapp-chat-api-go-2023-08-25T12-53-45.285.log", "newsapp-chat-api-go-2023-08-25T12-53-45.265.log", "newsapp-chat-api-go-2023-08-25T22-53-45.285.log"},
					"10.158.13.6:9187": {"newsapp-chat-user-go-2023-08-25T12-53-45.285.log", "newsapp-chat-user-go-2023-08-25T12-53-45.265.log", "newsapp-chat-user-go-2023-08-25T22-53-45.285.log"},
				},
			},
		}
		b, _ := json.Marshal(map[string]interface{}{
			"data": map[string]interface{}{
				"items": clusterNodes,
			},
			"code": 0,
		})
		w.Write(b)
	})
	log.Println("start server at :8081")
	err = http.ListenAndServe(":8081", mux)
	if err != nil {
		panic(err)
	}
	// curl --location --request GET '127.0.0.1:8081/search?kw=criteria&maxLines=5'
	// Output:
	// (vi) To be directly or tangibly associated with events or living traditions, with ideas, or with beliefs, with artistic and literary works of outstanding universal significance. (The Committee considers that this criterion should preferably be used in conjunction with other criteria)
	// For cultural sites, the following six criteria can apply:
}
