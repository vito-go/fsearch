package fsearch_test

import (
	"fmt"
	"github.com/vito-go/fsearch"
	"strings"
	"time"
)

func ExampleNewFileSearch() {

	search, err := fsearch.NewFileSearch("./testdata/log1.txt")
	if err != nil {
		panic(err)
	}
	time.Sleep(time.Second) //wait for search ready, in real project, you don't need this Sleep
	result := search.SearchFromEnd(10, "human creative genius")
	fmt.Println(strings.Join(result, "\n"))
	// Output:
	// (i) To represent a masterpiece of human creative genius;
}

func ExampleNewDirSearch() {
	search, err := fsearch.NewDirSearch("./testdata/")
	if err != nil {
		panic(err)
	}
	time.Sleep(time.Second) //wait for search ready, in real project, you don't need this Sleep
	result := search.SearchFromEnd(10, "criteria")
	// please note that the result's order is not guaranteed, actually it's by the order of file's last modified time
	fmt.Println(strings.Join(result, "\n"))
	// Output:
	// (vi) To be directly or tangibly associated with events or living traditions, with ideas, or with beliefs, with artistic and literary works of outstanding universal significance. (The Committee considers that this criterion should preferably be used in conjunction with other criteria)
	// For cultural sites, the following six criteria can apply:
}
