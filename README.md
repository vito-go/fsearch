# fsearch
Search text in files quickly(using linux mmap), especially for log searching. Directories are supported.

## Quick Start

### Server
```go
package main

import (
	"github.com/vito-go/fsearch/unilog"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	server := unilog.NewServer("/search", "/", "/ws", 9097)
	log.Println("server start: 9097")
	//bash: git clone github.com/vito-go/fsearch_flutter && flutter build web
	staticWebFile := http.Dir(filepath.Join(homeDir, "go/src/github.com/vito-go/fsearch_flutter/build/web"))
	server.Start(staticWebFile)
}

```
### Client

```go


package main

import (
	"github.com/vito-go/fsearch/unilog"
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
	searchDir := "github.com/vito-go/fsearch"
	hostName, _ := util.GetPrivateIP() //hostName can be any flag
	cli, err := unilog.NewClient(filepath.Join(homeDir, searchDir), appName, hostName)
	if err != nil {
		panic(err)
	}
	cli.RegisterToCenter("ws://127.0.0.1:9097/ws")
	// write here your own code instead of select {}
	select {}
}

```