package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/andybalholm/brotli"
)

type cacheFixtureEntry struct {
	key               string
	data              M
	external, longKey bool
}

func writeCacheFixture(t *testing.T, root string, entries ...cacheFixtureEntry) {
	t.Helper()
	check(t, os.MkdirAll(root, 0700))
	index := make([]byte, 372)
	binary.LittleEndian.PutUint32(index, 0xc103cac3)
	binary.LittleEndian.PutUint32(index[4:], 0x30000)
	binary.LittleEndian.PutUint32(index[28:], 1)
	files := map[string][]byte{}
	for name, unit := range map[string]int{"data_1": 256, "data_2": 1024, "data_3": 4096} {
		b := make([]byte, 8192+len(entries)*4*unit)
		binary.LittleEndian.PutUint32(b, 0xc104cac3)
		binary.LittleEndian.PutUint32(b[12:], uint32(unit))
		files[name] = b
	}
	if len(entries) > 0 {
		binary.LittleEndian.PutUint32(index[368:], 0xa3010000)
	}
	for j, fixture := range entries {
		entry := files["data_1"][8192+j*1024 : 8192+(j+1)*1024]
		if j+1 < len(entries) {
			binary.LittleEndian.PutUint32(entry[4:], 0xa3010000|uint32((j+1)*4))
		}
		binary.LittleEndian.PutUint64(entry[24:], uint64(j+1))
		binary.LittleEndian.PutUint32(entry[32:], uint32(len(fixture.key)))
		if fixture.longKey {
			binary.LittleEndian.PutUint32(entry[36:], 0x80000000|uint32(100+j))
			check(t, os.WriteFile(filepath.Join(root, "f_"+fmtCacheFile(100+j)), []byte(fixture.key), 0600))
		} else {
			copy(entry[96:], fixture.key)
		}
		info := []byte("HTTP/1.1 200\x00content-type:application/json\x00content-encoding:br\x00")
		var body bytes.Buffer
		writer := brotli.NewWriter(&body)
		_, err := writer.Write([]byte(jsonText(fixture.data)))
		check(t, err)
		check(t, writer.Close())
		binary.LittleEndian.PutUint32(entry[40:], uint32(len(info)))
		binary.LittleEndian.PutUint32(entry[44:], uint32(body.Len()))
		binary.LittleEndian.PutUint32(entry[56:], 0xb3020000|uint32(j*4))
		copy(files["data_2"][8192+j*4096:], info)
		if fixture.external {
			binary.LittleEndian.PutUint32(entry[60:], 0x80000000|uint32(j+1))
			check(t, os.WriteFile(filepath.Join(root, "f_"+fmtCacheFile(j+1)), body.Bytes(), 0600))
		} else {
			binary.LittleEndian.PutUint32(entry[60:], 0xc3030000|uint32(j*4))
			copy(files["data_3"][8192+j*16384:], body.Bytes())
		}
	}
	check(t, os.WriteFile(filepath.Join(root, "index"), index, 0600))
	for name, b := range files {
		check(t, os.WriteFile(filepath.Join(root, name), b, 0600))
	}
}

func fmtCacheFile(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, 6)
	for j := 5; j >= 0; j-- {
		b[j] = hex[n&15]
		n >>= 4
	}
	return string(b)
}
func cacheMessage(id, role, body string, ts int) M {
	return M{"id": id, "author": M{"role": role}, "create_time": ts, "content": M{"content_type": "text", "parts": []any{body}}}
}

func TestDesktopCacheBrotliPaginationAndExportDedup(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	root := t.TempDir()
	snapshot := M{"conversation_id": "desktop-one", "title": "小程序图片加载方式", "create_time": 100, "update_time": 103, "current_node": "a", "messages": []any{cacheMessage("q", "user", "本地缓存问题", 102), cacheMessage("a", "assistant", "图片懒加载", 103)}, "page_info": M{"has_previous_page": true}}
	page := M{"messages": []any{cacheMessage("early", "user", "早期问题", 101)}, "page_info": M{"has_previous_page": false}}
	writeCacheFixture(t, root,
		cacheFixtureEntry{key: "1/0/_dk_https://chatgpt.com https://chatgpt.com https://chatgpt.com/backend-api/conversations/desktop-one?num_turns=10", data: snapshot},
		cacheFixtureEntry{key: "https://chatgpt.com/backend-api/conversations/desktop-one/messages?before=q", data: page, external: true, longKey: true},
		cacheFixtureEntry{key: "https://example.com/backend-api/conversation/private", data: M{"messages": []any{cacheMessage("secret", "user", "不可导入其他域名", 1)}}})
	if !isDesktopCache(root) {
		t.Fatal("未识别桌面缓存")
	}
	before, _ := os.ReadFile(filepath.Join(root, "data_1"))
	source := Source{root, "Windows", true, desktopCacheKind}
	check(t, i.desktopChatGPT(source, false))
	check(t, i.desktopChatGPT(source, false))
	if count(t, s, "sessions") != 1 || count(t, s, "messages") != 3 {
		t.Fatal("分页未合并，或重复同步产生重复")
	}
	if i.status().Skipped != 2 {
		t.Fatal("未变化的缓存必须跳过")
	}
	after, _ := os.ReadFile(filepath.Join(root, "data_1"))
	if !bytes.Equal(before, after) {
		t.Fatal("修改了源缓存")
	}
	result, err := s.messages(context.Background(), "chatgpt:desktop-one", url.Values{})
	check(t, err)
	items := result["items"].([]M)
	if str(items[0]["body"]) != "早期问题" {
		t.Fatal("较早分页未按时间排在前面")
	}
	if str(obj(result["session"])["title"]) != "小程序图片加载方式" {
		t.Fatal("分页覆盖了标题")
	}
	// Importing the same message IDs from an official export merges with the cache.
	export := M{"id": "desktop-one", "title": "小程序图片加载方式", "create_time": 100, "update_time": 103, "current_node": "a", "mapping": M{}}
	nodes := obj(export["mapping"])
	previous := ""
	for _, m := range []M{cacheMessage("early", "user", "早期问题", 101), cacheMessage("q", "user", "本地缓存问题", 102), cacheMessage("a", "assistant", "图片懒加载", 103)} {
		id := str(m["id"])
		nodes[id] = M{"parent": previous, "message": m}
		previous = id
	}
	path := filepath.Join(t.TempDir(), "conversations.json")
	check(t, os.WriteFile(path, []byte(jsonText([]any{export})), 0600))
	check(t, i.chatgpt(path, false))
	if count(t, s, "messages") != 3 {
		t.Fatal("官方导出与桌面缓存重复收录")
	}
	// A shortened response must not discard messages from previously read pages.
	writeCacheFixture(t, root, cacheFixtureEntry{key: "https://chatgpt.com/backend-api/conversation/desktop-one", data: M{"conversation_id": "desktop-one", "title": "小程序图片加载方式", "messages": []any{cacheMessage("a", "assistant", "更新的答案", 103)}}})
	check(t, i.desktopChatGPT(source, false))
	if count(t, s, "messages") != 3 {
		t.Fatal("较短缓存导致历史丢失")
	}
	if count(t, s, "raw_chunks") < 4 {
		t.Fatal("未保留缓存及导出的原始版本")
	}
}

func TestDesktopCacheTruncationAndInvalidPayloadDoNotCheckpoint(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	root := t.TempDir()
	entry := cacheFixtureEntry{key: "https://chatgpt.com/backend-api/conversation/expected", data: M{"conversation_id": "wrong", "messages": []any{cacheMessage("q", "user", "错误会话", 1)}}}
	writeCacheFixture(t, root, entry)
	source := Source{root, "Windows", true, desktopCacheKind}
	check(t, i.desktopChatGPT(source, false))
	if count(t, s, "messages") != 0 || count(t, s, "files") != 0 || len(i.status().Warnings) == 0 {
		t.Fatal("ID 不匹配的缓存被导入或静默跳过")
	}
	entry.data["conversation_id"] = "expected"
	writeCacheFixture(t, root, entry)
	check(t, os.Truncate(filepath.Join(root, "data_3"), 8192))
	check(t, i.desktopChatGPT(source, false))
	if count(t, s, "files") != 0 {
		t.Fatal("未写完的响应不应设置检查点")
	}
	writeCacheFixture(t, root, entry)
	check(t, i.desktopChatGPT(source, false))
	if count(t, s, "messages") != 1 {
		t.Fatal("缓存补写后无法重试")
	}
}

func TestConversationCacheURLIgnoresOtherRequests(t *testing.T) {
	for _, key := range []string{"https://evil.example/backend-api/conversation/abc", "https://chatgpt.com/backend-api/conversations", "https://chatgpt.com/backend-api/conversation/abc/executor/file", "https://chatgpt.com/backend-api/conversation/%2e%2e", "https://chatgpt.com/backend-api/conversation/abc/stream_status"} {
		if _, sid := conversationCacheURL(key); sid != "" {
			t.Fatalf("接受了无关请求 %s", key)
		}
	}
}

// Optional real-cache check, using an isolated archive and no source writes.
func TestDesktopCacheLiveProbe(t *testing.T) {
	path := os.Getenv("ECHO_DESKTOP_CACHE_TEST_PATH")
	if path == "" {
		t.Skip("set ECHO_DESKTOP_CACHE_TEST_PATH to verify a real desktop cache")
	}
	s := testStore(t)
	i := newImporter(s)
	check(t, i.desktopChatGPT(Source{path, "Windows", true, desktopCacheKind}, false))
	t.Logf("imported %d sessions / %d records, %d warnings", count(t, s, "sessions"), count(t, s, "messages"), len(i.status().Warnings))
	if sid := os.Getenv("ECHO_DESKTOP_CACHE_TEST_ID"); sid != "" {
		row, err := one(s.DB, "SELECT title,message_count,total_count FROM sessions WHERE id=?", "chatgpt:"+sid)
		check(t, err)
		t.Logf("target: %s, %d dialogue messages / %d total records", str(row["title"]), integer(row["message_count"]), integer(row["total_count"]))
		if integer(row["total_count"]) == 0 {
			t.Fatal("target cache body was not imported")
		}
	}
	before := count(t, s, "messages")
	check(t, i.desktopChatGPT(Source{path, "Windows", true, desktopCacheKind}, false))
	if count(t, s, "messages") != before {
		t.Fatal("second live sync duplicated messages")
	}
}
