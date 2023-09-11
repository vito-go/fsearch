package main

import (
	"github.com/vito-go/fsearch/unilog"
	"github.com/vito-go/fsearch/util"
	"log"
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
	searchDir := "../testdata"         // can be any directory, especially for logs/
	hostName, _ := util.GetPrivateIP() //hostName can be any flag, but using ip is better
	cli, err := unilog.NewClient(searchDir, appName, hostName)
	if err != nil {
		panic(err)
	}
	cli.RegisterToCenter("ws://127.0.0.1:9097/ws")
	//cli.RegisterToCenter("ws://vitogo.tpddns.cn:9097/ws")

	// use http to search, param is kw and files, multi kw and multi files are supported
	// cli.RegisterWithHTTP(8097, "/search") // uncomment this line if you want to use http to search
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
	registerPath := "/ws"   // the path that client register to: ws://127.0.0.1:9097/ws
	searchPath := "/search" // api for search
	server := unilog.NewServer(searchPath, "/", registerPath, 9097)
	log.Println("server start: 9097")
	//wget https://github.com/vito-go/fsearch_flutter/releases/download/v0.0.1/web.zip
	//unzip web.zip
	// the staticDir is that you download and unzip above, more details in README.md
	staticDir := "web"
	staticWebFile := http.Dir(staticDir)
	log.Fatalln(server.Start(staticWebFile))
}
