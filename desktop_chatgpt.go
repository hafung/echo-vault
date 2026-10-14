package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"
)

const desktopCacheKind = "ChatGPT Desktop"
const cacheReadLimit = 32 << 20
const desktopPartialCacheWarning = "磁盘缓存只包含部分分页；已保存可读取的正文，可在数据与来源启用桌面主动补全，或通过官方导出补充历史"

// Chromium's blockfile HTTP cache. Only the active index and conversation response
// streams are read; authentication stores, cookies and unrelated bodies are ignored.
type desktopCache struct{ root string }
type cachedConversation struct {
	key, sid string
	created  uint64
	payload  M
	raw      []byte
}

func desktopCachePaths() []string {
	home, _ := os.UserHomeDir()
	paths := []string{}
	patterns := []string{}
	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		patterns = append(patterns, filepath.Join(local, "Packages", "OpenAI.*", "LocalCache", "Roaming", "*", "web", "*", "Default", "Cache", "Cache_Data"))
		patterns = append(patterns, filepath.Join(os.Getenv("APPDATA"), "Codex", "web", "Codex", "Default", "Cache", "Cache_Data"))
	} else {
		patterns = append(patterns, "/mnt/*/Users/*/AppData/Local/Packages/OpenAI.*/LocalCache/Roaming/*/web/*/Default/Cache/Cache_Data")
		patterns = append(patterns, "/mnt/*/Users/*/AppData/Roaming/Codex/web/Codex/Default/Cache/Cache_Data")
		patterns = append(patterns, filepath.Join(home, ".config", "Codex", "web", "Codex", "Default", "Cache", "Cache_Data"))
		patterns = append(patterns, filepath.Join(home, "Library", "Application Support", "Codex", "web", "Codex", "Default", "Cache", "Cache_Data"))
	}
	for _, pattern := range patterns {
		paths = append(paths, glob(pattern)...)
	}
	return paths
}

func isDesktopCache(path string) bool {
	f, err := os.Open(filepath.Join(path, "index"))
	if err != nil {
		return false
	}
	defer f.Close()
	var header [8]byte
	_, err = io.ReadFull(f, header[:])
	return err == nil && binary.LittleEndian.Uint32(header[:4]) == 0xc103cac3
}

func (c desktopCache) read(addr uint32, size int) ([]byte, error) {
	if addr&0x80000000 == 0 || size < 0 || size > cacheReadLimit {
		return nil, errors.New("无效的缓存地址或大小")
	}
	kind := (addr >> 28) & 7
	name := fmt.Sprintf("f_%06x", addr&0x0fffffff)
	var offset int64
	if kind != 0 {
		units := map[uint32]int64{1: 36, 2: 256, 3: 1024, 4: 4096}
		unit, ok := units[kind]
		if !ok || int64(size) > unit*int64(((addr>>24)&3)+1) {
			return nil, errors.New("缓存块大小不匹配")
		}
		name = fmt.Sprintf("data_%d", (addr>>16)&255)
		offset = 8192 + int64(addr&65535)*unit
	}
	f, err := os.Open(filepath.Join(c.root, name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if kind != 0 {
		var header [16]byte
		if _, err = f.ReadAt(header[:], 0); err != nil {
			return nil, err
		}
		if binary.LittleEndian.Uint32(header[:4]) != 0xc104cac3 {
			return nil, errors.New("不是 Chromium 缓存块")
		}
		if binary.LittleEndian.Uint32(header[12:]) != uint32(map[uint32]int{1: 36, 2: 256, 3: 1024, 4: 4096}[kind]) {
			return nil, errors.New("缓存块类型不匹配")
		}
	}
	data := make([]byte, size)
	_, err = f.ReadAt(data, offset)
	return data, err
}

func conversationCacheURL(key string) (string, string) {
	// Cache partition prefixes contain origins; the final field is the request URL.
	fields := strings.Fields(key)
	if len(fields) == 0 {
		return "", ""
	}
	request := fields[len(fields)-1]
	u, err := url.Parse(request)
	if err != nil || u.Scheme != "https" || u.Host != "chatgpt.com" {
		return "", ""
	}
	path := u.Path
	for _, prefix := range []string{"/backend-api/", "/__codex-api/", "/api/"} {
		path = strings.TrimPrefix(path, prefix)
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || len(parts) > 3 || (parts[0] != "conversation" && parts[0] != "conversations") || (len(parts) == 3 && parts[2] != "messages") {
		return "", ""
	}
	sid := parts[1]
	if sid == "" || len(sid) > 128 {
		return "", ""
	}
	for _, r := range sid {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return "", ""
		}
	}
	return request, sid
}

func decodeCacheBody(info, body []byte) ([]byte, error) {
	encoding := ""
	ok := false
	for _, h := range bytes.Split(info, []byte{0}) {
		line := strings.TrimSpace(strings.ToLower(string(h)))
		if strings.HasPrefix(line, "http/") && strings.Contains(line, " 200") {
			ok = true
		}
		if strings.HasPrefix(line, "content-encoding:") {
			encoding = strings.TrimSpace(strings.TrimPrefix(line, "content-encoding:"))
		}
	}
	if !ok {
		return nil, errors.New("缓存响应不是 HTTP 200")
	}
	var reader io.Reader = bytes.NewReader(body)
	var closer io.Closer
	switch encoding {
	case "", "identity":
	case "br":
		reader = brotli.NewReader(reader)
	case "gzip":
		r, err := gzip.NewReader(reader)
		if err != nil {
			return nil, err
		}
		reader, closer = r, r
	case "deflate":
		r, err := zlib.NewReader(reader)
		if err != nil {
			r = flate.NewReader(bytes.NewReader(body))
		}
		reader, closer = r, r
	default:
		return nil, fmt.Errorf("暂不支持缓存压缩格式 %s", encoding)
	}
	if closer != nil {
		defer closer.Close()
	}
	raw, err := io.ReadAll(io.LimitReader(reader, cacheReadLimit+1))
	if len(raw) > cacheReadLimit {
		return nil, errors.New("缓存解压后超过 32 MB")
	}
	return raw, err
}

func (c desktopCache) conversations(report func(string, error)) ([]cachedConversation, error) {
	index, err := os.ReadFile(filepath.Join(c.root, "index"))
	if err != nil {
		return nil, err
	}
	if len(index) < 368 || binary.LittleEndian.Uint32(index[:4]) != 0xc103cac3 {
		return nil, errors.New("不是 Chromium blockfile 缓存索引")
	}
	version := binary.LittleEndian.Uint32(index[4:8])
	if version != 0x20000 && version != 0x20001 && version != 0x30000 {
		return nil, fmt.Errorf("暂不支持 Chromium 缓存索引版本 %x", version)
	}
	tableLen := binary.LittleEndian.Uint32(index[28:32])
	if tableLen == 0 {
		tableLen = 65536
	}
	if tableLen > 1<<20 || 368+int(tableLen)*4 > len(index) {
		return nil, errors.New("缓存索引截短或过大")
	}
	seen := map[uint32]bool{}
	result := []cachedConversation{}
	for p := 368; p < 368+int(tableLen)*4; p += 4 {
		addr := binary.LittleEndian.Uint32(index[p : p+4])
		for addr != 0 && !seen[addr] {
			if len(seen) > 1<<20 {
				return nil, errors.New("缓存索引条目过多")
			}
			seen[addr] = true
			if (addr>>28)&7 != 2 {
				break
			}
			entry, e := c.read(addr, 96)
			if e != nil {
				break
			} // Eviction can race an active index snapshot.
			next := binary.LittleEndian.Uint32(entry[4:8])
			current := addr
			addr = next
			if binary.LittleEndian.Uint32(entry[20:24]) != 0 {
				continue
			}
			keyLen := int(binary.LittleEndian.Uint32(entry[32:36]))
			if keyLen <= 0 || keyLen > 64<<10 {
				continue
			}
			longKey := binary.LittleEndian.Uint32(entry[36:40])
			var key []byte
			if longKey != 0 {
				key, e = c.read(longKey, keyLen)
			} else {
				var data []byte
				data, e = c.read(current, 96+keyLen)
				if e == nil {
					key = data[96:]
				}
			}
			if e != nil {
				continue
			}
			request, sid := conversationCacheURL(string(key))
			if sid == "" {
				continue
			}
			var streams [2][]byte
			for j := 0; j < 2; j++ {
				n := int(int32(binary.LittleEndian.Uint32(entry[40+j*4 : 44+j*4])))
				streamAddr := binary.LittleEndian.Uint32(entry[56+j*4 : 60+j*4])
				if n <= 0 {
					e = errors.New("缓存响应尚未写完")
					break
				}
				streams[j], e = c.read(streamAddr, n)
				if e != nil {
					break
				}
			}
			if e != nil {
				report(request, e)
				continue
			}
			// Validate selected bytes twice to avoid combining an evicted/reused entry
			// with another response while Desktop is still writing the cache.
			afterSize := 96
			if longKey == 0 {
				afterSize += keyLen
			}
			after, e := c.read(current, afterSize)
			if e != nil || !bytes.Equal(after[:96], entry) || (longKey == 0 && !bytes.Equal(after[96:], key)) {
				report(request, errors.New("缓存正在变化，下次同步重试"))
				continue
			}
			if longKey != 0 {
				again, err := c.read(longKey, keyLen)
				if err != nil || !bytes.Equal(again, key) {
					report(request, errors.New("缓存键正在变化，下次同步重试"))
					continue
				}
			}
			stable := true
			for j := 0; j < 2; j++ {
				again, e := c.read(binary.LittleEndian.Uint32(entry[56+j*4:60+j*4]), len(streams[j]))
				if e != nil || !bytes.Equal(again, streams[j]) {
					stable = false
					break
				}
			}
			if !stable {
				report(request, errors.New("缓存响应正在变化，下次同步重试"))
				continue
			}
			raw, e := decodeCacheBody(streams[0], streams[1])
			if e != nil {
				report(request, e)
				continue
			}
			var payload M
			if e = json.Unmarshal(raw, &payload); e != nil {
				report(request, e)
				continue
			}
			id := str(first(payload["conversation_id"], payload["id"]))
			if id != "" && id != sid {
				report(request, errors.New("缓存正文会话 ID 与请求不一致"))
				continue
			}
			if _, mapping := payload["mapping"].(map[string]any); !mapping {
				if _, messages := payload["messages"].([]any); !messages {
					continue
				}
			}
			result = append(result, cachedConversation{request, sid, binary.LittleEndian.Uint64(entry[24:32]), payload, raw})
		}
	}
	sort.SliceStable(result, func(a, b int) bool { return result[a].created < result[b].created })
	return result, nil
}

func (i *Importer) desktopChatGPT(source Source, full bool) error {
	i.Store.importMu.Lock()
	defer i.Store.importMu.Unlock()
	entries, err := (desktopCache{source.Path}).conversations(func(path string, err error) { i.issue(path, err, true) })
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := source.Path + "::" + hash([]byte(entry.key))
		digest := hash(entry.raw)
		if !full {
			previous, e := one(i.Store.DB, "SELECT head_hash FROM files WHERE path=?", path)
			if e == nil && str(previous["head_hash"]) == digest {
				i.update(func(s *SyncStatus) { s.Skipped++ })
				continue
			}
		}
		c := entry.payload
		c["conversation_id"] = entry.sid
		// Separate page responses carry messages but no title or conversation times.
		if str(c["title"]) == "" {
			if row, e := one(i.Store.DB, "SELECT title,created,updated,model FROM sessions WHERE id=?", "chatgpt:"+entry.sid); e == nil {
				c["title"], c["create_time"], c["update_time"] = row["title"], row["created"], row["updated"]
				c["default_model_slug"] = row["model"]
			}
		}
		if stamp(c["create_time"]) == 0 {
			for _, value := range array(c["messages"]) {
				t := stamp(obj(value)["create_time"])
				if t > 0 && (stamp(c["create_time"]) == 0 || t < stamp(c["create_time"])) {
					c["create_time"] = t
				}
			}
		}
		generation, _ := strconv.ParseUint(digest[:15], 16, 64)
		n, err := i.chatgptConversation(c, path, int64(generation), full, source.Platform, entry.raw)
		if err != nil {
			return err
		}
		_, err = i.Store.Write.Exec("INSERT INTO files(path,session_id,head_hash,mode,last_sync) VALUES (?,?,?,'chatgpt_desktop',?) ON CONFLICT(path) DO UPDATE SET head_hash=excluded.head_hash,last_sync=excluded.last_sync,missing=0", path, "chatgpt:"+entry.sid, digest, now())
		if err != nil {
			return err
		}
		i.update(func(s *SyncStatus) { s.Imported += n })
		page := obj(c["page_info"])
		if page["has_previous_page"] == true || page["has_next_page"] == true {
			row, e := one(i.Store.DB, "SELECT metadata FROM sessions WHERE id=?", "chatgpt:"+entry.sid)
			var meta M
			if e == nil {
				json.Unmarshal([]byte(str(row["metadata"])), &meta)
			}
			if meta["desktop_capture_complete"] != true {
				i.issue(str(first(c["title"], entry.sid)), errors.New(desktopPartialCacheWarning), true)
			}
		}
	}
	if len(entries) == 0 {
		i.issue(source.Path, errors.New("尚无可读取的 Chat 正文缓存；在桌面应用中打开对话后再同步，已收录历史继续保留"), true)
	}
	return nil
}
