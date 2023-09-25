package fsearch

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type DirSearch struct {
	rwMutex sync.RWMutex
	fileMap map[string]*FileSearch
	dir     string

	closeOnce sync.Once // protect closeChan
	//closed    atomic.Bool // go1.19
	closed int64 // 0 false 1 true,for version less than go1.19
}

func NewDirSearch(dir string) (*DirSearch, error) {
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return nil, err
	}
	f := &DirSearch{
		dir:     dir,
		fileMap: make(map[string]*FileSearch, 64),
	}
	go f.detectDir()
	return f, nil
}
func (f *DirSearch) Close() {
	f.closeOnce.Do(func() {
		//f.closed.Store(true)
		atomic.StoreInt64(&f.closed, 1)
		for _, search := range f.fileMap {
			search.Close()
		}
	})
}
func (f *DirSearch) IsClosed() bool {
	//return f.closed.Load()
	return atomic.LoadInt64(&f.closed) == 1
}
func (f *DirSearch) Files() []string {
	f.rwMutex.RLock()
	defer f.rwMutex.RUnlock()
	var files []string
	for fileName := range f.fileMap {
		files = append(files, fileName)
	}
	return files
}

func (f *DirSearch) SearchFromLastFile(maxLines int, fromEnd bool, kws ...string) []string {
	var lastFileSearch *FileSearch
	var lastTime time.Time
	f.rwMutex.RLock()
	for _, s := range f.fileMap {
		if s.updateTime.After(lastTime) {
			lastTime = s.updateTime
			lastFileSearch = s
		}
	}
	f.rwMutex.RUnlock()
	if lastFileSearch == nil {
		return nil
	}
	if fromEnd {
		return lastFileSearch.SearchFromEnd(maxLines, kws...)
	}
	return lastFileSearch.SearchFromStart(maxLines, kws...)
}
func (f *DirSearch) SearchWithFileName(maxLines int, fileName string, kws ...string) []string {
	f.rwMutex.RLock()
	defer f.rwMutex.RUnlock()
	search, ok := f.fileMap[fileName]
	if !ok {
		return nil
	}
	lines := search.SearchFromEnd(maxLines, kws...)
	return lines
}
func (f *DirSearch) SearchFromStart(maxLines int, kws ...string) []string {
	f.rwMutex.RLock()
	defer f.rwMutex.RUnlock()
	var result []string
	var searches []*FileSearch
	for _, search := range f.fileMap {
		searches = append(searches, search)
	}
	sort.Slice(searches, func(i, j int) bool {
		return searches[i].updateTime.After(searches[j].updateTime)
	})
	for _, search := range searches {
		lines := search.SearchFromStart(maxLines, kws...)
		result = append(result, lines...)
	}
	return result
}
func (f *DirSearch) SearchFromEnd(maxLines int, kws ...string) []string {
	f.rwMutex.RLock()
	defer f.rwMutex.RUnlock()
	var result []string
	var searches []*FileSearch
	for _, search := range f.fileMap {
		searches = append(searches, search)
	}
	sort.Slice(searches, func(i, j int) bool {
		return searches[i].updateTime.After(searches[j].updateTime)
	})
	for _, search := range searches {
		lines := search.SearchFromEnd(maxLines, kws...)
		result = append(result, lines...)
	}
	return result
}
func (f *DirSearch) SearchFromEndWithFiles(maxLines int, fileMap map[string]bool, kws ...string) []string {
	f.rwMutex.RLock()
	defer f.rwMutex.RUnlock()
	var result []string
	var searches []*FileSearch
	for _, search := range f.fileMap {
		if len(fileMap) > 0 {
			if fileMap[filepath.Base(search.fileName)] {
				searches = append(searches, search)
			}
			continue
		}
		searches = append(searches, search)
	}
	sort.Slice(searches, func(i, j int) bool {
		return searches[i].updateTime.After(searches[j].updateTime)
	})
	for _, search := range searches {
		lines := search.SearchFromEnd(maxLines, kws...)
		result = append(result, lines...)
	}
	return result
}

func (f *DirSearch) SearchFromEndAndWrite(writer io.Writer, hostName string, maxLines int, fileMap map[string]bool, kws ...string) {
	f.rwMutex.RLock()
	defer f.rwMutex.RUnlock()
	var searches []*FileSearch
	for _, search := range f.fileMap {
		if len(fileMap) > 0 {
			if fileMap[filepath.Base(search.fileName)] {
				searches = append(searches, search)
			}
			continue
		}
		searches = append(searches, search)
	}
	sort.Slice(searches, func(i, j int) bool {
		return searches[i].updateTime.After(searches[j].updateTime)
	})

	for _, search := range searches {
		lines := search.SearchFromEnd(maxLines, kws...)
		if len(lines) == 0 {
			continue
		}
		writer.Write([]byte(fmt.Sprintf("<<<<<< --------------------%s %s -------------------- >>>>>>\n", hostName, filepath.Base(search.fileName))))
		_, _ = writer.Write([]byte(strings.Join(lines, "\n\n") + "\n\n"))
	}
	return
}

func (f *DirSearch) SearchBy(maxLines int, end bool, fileNames []string, kws ...string) []string {
	if len(fileNames) == 0 || len(kws) == 0 {
		return nil
	}
	f.rwMutex.RLock()
	defer f.rwMutex.RUnlock()
	var result []string
	var searches []*FileSearch
	for _, search := range f.fileMap {
		searches = append(searches, search)
	}
	sort.Slice(searches, func(i, j int) bool {
		return searches[i].updateTime.After(searches[j].updateTime)
	})
	for _, search := range searches {
		var lines []string
		if end {
			lines = search.SearchFromEnd(maxLines, kws...)
		} else {
			lines = search.SearchFromStart(maxLines, kws...)
		}
		result = append(result, lines...)
	}
	return result
}

var textExtensions = []string{
	".txt",
	".log",
	".md",
	".go",
	".java",
	".js",
	".html",
	".css",
	".json",
	".xml",
	".yaml",
	".yml",
	".ini",
	".conf",
	".properties",
	".sh",
	".bat",
	".cmd",
	".php",
	".py",
	".c",
	".cpp",
	".h",
	".hpp",
	".cc",
	".hh",
	".java",
	".cs",
	".ts",
	".tsx",
	".vue",
	".sql",
	".rb",
	".pl",
	".pm",
	".t",
	".rs",
	".swift",
	".scala",
	".groovy",
	".kt",
	".kts",
	".clj",
	".cljs",
	".cljc",
	".coffee",
	".dart",
	".erl",
	".hrl",
}

func checkExt(fileName string) bool {
	ext := filepath.Ext(fileName)
	for _, e := range textExtensions {
		if e == ext {
			return true
		}
	}
	return false
}

func (f *DirSearch) detectDir() {
	defer f.Close()
	for {
		if f.IsClosed() {
			return
		}
		entries, err := os.ReadDir(f.dir)
		if err != nil {
			log.Println(err)
			return
		}
		entryNameMap := make(map[string]bool, len(entries))
		for _, entry := range entries {
			if !checkEntry(entry) {
				continue
			}
			entryNameMap[entry.Name()] = true
		}
		// 先剔除不存在的文件
		f.rwMutex.Lock()
		for s, search := range f.fileMap {
			if _, ok := entryNameMap[s]; !ok {
				search.Close()
				delete(f.fileMap, s)
				log.Println("fsearch: remove file", s)
			}
		}
		// 再添加新的文件
		for _, entry := range entries {
			if !checkEntry(entry) {
				continue
			}
			fileName := entry.Name()
			if _, ok := f.fileMap[fileName]; ok {
				continue
			}
			filePath := filepath.Join(f.dir, fileName)
			fileSearch, err := NewFileSearch(filePath)
			if err != nil {
				log.Println(err)
				continue
			}
			f.fileMap[fileName] = fileSearch
		}
		f.rwMutex.Unlock()
		time.Sleep(time.Second * 30)
	}
}
func checkEntry(entry os.DirEntry) bool {
	if entry.IsDir() {
		return false
	}
	if strings.HasPrefix(entry.Name(), ".") {
		return false
	}
	if !strings.Contains(entry.Name(), ".") {
		return false
	}
	if !checkExt(entry.Name()) {
		return false
	}
	return true
}
