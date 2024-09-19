package main

import (
	"github.com/vito-go/fsearch"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

func init() {
	// Change current working directory to the directory of this file
	_, file, _, _ := runtime.Caller(0)
	err := os.Chdir(filepath.Dir(file))
	if err != nil {
		panic(err)
	}
}

func main() {
	clientRegister()
	serverStart()
}

// clientRegister register client to center
// it's async, so you can start this first. no need to use goroutine
// if connection failed, it will retry every 10 seconds
func clientRegister() {
	appName := "demoApp"
	searchDir := "../testdata" // can be any directory, especially for logs/
	cli, err := fsearch.NewClient(searchDir, appName)
	if err != nil {
		panic(err)
	}
	centerWSAddr := "ws://127.0.0.1:9097/ws"
	cli.RegisterToCenter(centerWSAddr)
	// use http to search, param is kw and files, multi kw and multi files are supported
	// query: kw, files, maxLines
	if err = cli.RegisterWithHTTP(8097, "/search"); err != nil {
		panic(err)
	} // comment this line if you do not want to use http to search
	/*
		when you use http to search, you can use curl to test, or open the url directly in the browser
		curl --location --request GET 'http://127.0.0.1:8097/search?kw=outstanding&kw=associated'
			outputs:
			<<<<<< --------------------10.236.148.250 log2.txt -------------------- >>>>>>
		(vi) To be directly or tangibly associated with events or living traditions, with ideas, or with beliefs, with artistic and literary works of outstanding universal significance. (The Committee considers that this criterion should preferably be used in conjunction with other criteria)

	*/
	// curl --location --request GET 'http://127.0.0.1:8097/search?kw=outstanding'
}

// serverStart start server.
func serverStart() {
	wsRegisterPath := "/ws" // the path that client register to: ws://127.0.0.1:9097/ws
	var indexPath string
	indexPath = "/" // index path must end with /, usually it's a single slash /
	//indexPath = "/" // index path must end with /
	var authMap map[string]*fsearch.AccountConfig
	//authMap = map[string]*fsearch.AccountConfig{
	//	"test": &fsearch.AccountConfig{
	//		Username:         "test",
	//		Password:         "test",
	//		AllowedAppNames:  nil,
	//		ExcludedAppNames: []string{"demoApp"},
	//	},
	//}
	//authMap can be nil, if it's nil, no auth is required
	// if excludedAppNames is not nil, then it will be used to exclude some appNames
	server := fsearch.NewServer(indexPath, wsRegisterPath, authMap)
	//wget https://github.com/vito-go/fsearch_flutter/releases/download/v0.0.2/web.zip
	//unzip web.zip
	// the staticDir is that you download and unzip above, more details in README.md
	staticDir := "web"
	staticWebFile := http.Dir(staticDir)

	err := server.StartListenAndServe(staticWebFile, ":9097")
	if err != nil {
		panic(err)
	}
}
