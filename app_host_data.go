package fsearch

import (
	"sort"
	"sync"
)

type appHostData struct {
	mux  sync.RWMutex
	data map[string]map[uint64]*NodeInfo // appName: NodeId:*NodeInfo
}

type NodeInfo struct {
	NodeId   uint64   `json:"nodeId"`
	HostName string   `json:"hostName"`
	Files    []string `json:"files"`
}
type ClusterNode struct {
	AppName   string     `json:"appName,omitempty"`
	AllFiles  []string   `json:"allFiles"` // all files after deduplication
	NodeInfos []NodeInfo `json:"nodeInfos"`
}

func (a *appHostData) Set(appName string, nodeId uint64, hostName string, files []string) {
	a.mux.Lock()
	defer a.mux.Unlock()
	if _, ok := a.data[appName]; !ok {
		a.data[appName] = make(map[uint64]*NodeInfo)
	}
	a.data[appName][nodeId] = &NodeInfo{HostName: hostName, NodeId: nodeId, Files: files}
}

func (a *appHostData) Del(appName string, id uint64) {
	a.mux.Lock()
	defer a.mux.Unlock()
	if _, ok := a.data[appName]; !ok {
		return
	}
	delete(a.data[appName], id)
	if len(a.data[appName]) == 0 {
		delete(a.data, appName)
	}
}
func (a *appHostData) ExistsBy(appName string, uid uint64) bool {
	a.mux.RLock()
	defer a.mux.RUnlock()
	if _, ok := a.data[appName]; !ok {
		return false
	}
	_, ok := a.data[appName][uid]
	return ok
}

func (a *appHostData) GetHostNodeInfoMap(appName string) map[uint64]NodeInfo {
	a.mux.RLock()
	defer a.mux.RUnlock()
	if _, ok := a.data[appName]; !ok {
		return nil
	}
	var result = make(map[uint64]NodeInfo, len(a.data[appName]))
	for k, v := range a.data[appName] {
		result[k] = *v
	}
	return result
}
func (a *appHostData) GetHostNodeUniqueIds(appName string) []uint64 {
	a.mux.RLock()
	defer a.mux.RUnlock()
	if _, ok := a.data[appName]; !ok {
		return nil
	}
	var result = make([]uint64, 0, len(a.data[appName]))
	for k := range a.data[appName] {
		result = append(result, k)
	}
	return result
}

func (a *appHostData) GetClusterNodes() []ClusterNode {
	a.mux.RLock()
	defer a.mux.RUnlock()
	if len(a.data) == 0 {
		return nil
	}
	items := make([]ClusterNode, 0, len(a.data))
	for appName, hostsNode := range a.data {
		fileMap := make(map[string]struct{}, len(hostsNode))
		var nodeInfos []NodeInfo
		for _, info := range hostsNode {
			nodeInfos = append(nodeInfos, *info)
			for _, file := range info.Files {
				fileMap[file] = struct{}{}
			}
		}
		allFiles := make([]string, 0, len(fileMap))
		for file := range fileMap {
			allFiles = append(allFiles, file)
		}
		sort.Strings(allFiles)
		clusterNode := ClusterNode{
			AppName:   appName,
			NodeInfos: nodeInfos,
			AllFiles:  allFiles,
		}
		items = append(items, clusterNode)
	}
	// sort by appName
	sort.Slice(items, func(i, j int) bool {
		return items[i].AppName < items[j].AppName
	})
	return items
}
