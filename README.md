# fsearch

Search text in files quickly(using linux grep command), especially for log searching. Directories are supported.
Support local remote online registration search and single machine search.

## Quick Start
- Look at the example directory for more details.
### Server

```shell
	wget https://github.com/vito-go/fsearch_flutter/releases/download/v0.0.2/web.zip
	unzip web.zip

```

```go
package main

import (
	"github.com/vito-go/fsearch"
	"log"
	"net/http"
)
// uncomment this if you want to use embed file
/*
//go:embed web
var staticEmbededFile embed.FS
*/
func main() {
	server := fsearch.NewServer("/search", "/", "/ws")
	log.Println("server start: 9097")
	// the dir is that you download and unzip above 
	staticWebFile := http.Dir("web")
	// you can also use embed file here, but you need to uncomment the code above and import embed
	// e.g staticWebFile := http.FS(staticEmbededFile)
	server.StartListenAndServe(staticWebFile, ":9097")
}

```

### Client

```go


package main

import (
	"github.com/vito-go/fsearch"
	"github.com/vito-go/fsearch/util"
	"os"
	"path/filepath"
)

func main() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	appName := "demoApp"
	searchDir := "github.com/vito-go/fsearch" // can be any directory, especially for logs/ 
	hostName, _ := util.GetPrivateIP()        //hostName can be any flag
	cli, err := fsearch.NewClient(filepath.Join(homeDir, searchDir), appName, hostName)
	if err != nil {
		panic(err)
	}
	cli.RegisterToCenter("ws://127.0.0.1:9097/ws")
	//cli.RegisterToCenter("ws://vitogo.tpddns.cn:9097/ws")
	// write here your own code instead of select {}
	select {}
}

```

## Demo

<img src="./images/fsearch.png" />
<img src="./images/fsearch1.png" />
<img src="./images/fsearch2.png" />

## TODO

- auth for each app