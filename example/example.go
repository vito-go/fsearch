package main

import (
	"github.com/vito-go/fsearch"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	search, err := fsearch.NewDirSearch("../testdata/")
	if err != nil {
		panic(err)
	}
	const defaultMaxLines = 20
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		kws := query["kw"]
		log.Println("kws is=>", kws)
		maxLines, err := strconv.Atoi(query.Get("maxLines"))
		if err != nil {
			maxLines = defaultMaxLines
		}
		lines := search.SearchFromEnd(maxLines, kws...)
		for _, line := range lines {
			_, _ = writer.Write([]byte(line + "\n"))
		}
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
