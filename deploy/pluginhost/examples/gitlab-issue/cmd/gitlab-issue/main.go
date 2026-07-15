// gitlab-issue demo plugin 的 subprocess 入口。
//
// 协议(由 ongrid pluginhost 的 subprocess runtime 定义,见
// internal/pluginhost/runtime/subprocess.go):
//
//	1) B → C(stdin,一行一帧 \n 分隔):
//	   {"id":"req-42", "cap":"create_issue", "params":{...}}
//
//	2) C → B(stdout,一行一帧 \n 分隔):
//	   成功:{"id":"req-42", "result":{...}}
//	   失败:{"id":"req-42", "error":"..."}
//
// 当前 demo 不真的去 gitlab.com 调 API,只把入参包成"已创建"结果回出去,证明整条链
// (B 拉起 C → JSON-RPC → 解析 → 回写 → registry / adapter / server 全链路)能跑通。
// 真实接入 GitLab API 的代码留到 Phase 6+ 与 GitLab token vault 集成阶段一起做。
//
// 运行模式:
//   - 默认(无参数):stdin/stdout JSON-RPC,适合被 B 的 SubprocessRuntime 拉起
//   - `--self-test`:本地冒烟测试,在 stdout 上跑一遍 envelope in/out 然后退出
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// stdioEnvelope 与 internal/pluginhost/runtime/subprocess.go stdioEnvelope
// 字段完全一致(本进程不 import runtime 包,避免 demo plugin 反向依赖 host 主项目)。
type stdioEnvelope struct {
	ID     string          `json:"id"`
	Cap    string          `json:"cap"`
	Params json.RawMessage `json:"params,omitempty"`
}

type stdioResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func main() {
	selfTest := flag.Bool("self-test", false, "run a local smoke test and exit")
	flag.Parse()

	if *selfTest {
		runSelfTest()
		return
	}

	// 1. 一行一个 envelope,读到 EOF 退出
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for in.Scan() {
		line := bytesTrim(in.Bytes())
		if len(line) == 0 {
			continue
		}
		resp := handleRequest(line)
		if err := writeFrame(out, resp); err != nil {
			fmt.Fprintf(os.Stderr, "gitlab-issue: write response: %v\n", err)
			os.Exit(1)
		}
	}
	if err := in.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "gitlab-issue: read stdin: %v\n", err)
		os.Exit(1)
	}
}

// handleRequest 把一行 envelope 解析 + dispatch,并返回响应帧。
//
// 已知 limitation:
//   - 真实 GitLab 调用需要 token,plugin 当前不接 vault,token 只能由调用方通过
//     env(GITLAB_TOKEN)塞进来;Phase 6+ 接入 host.call vault.get_ref 后,token
//     不再以明文走 env。
func handleRequest(raw []byte) stdioResponse {
	var env stdioEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		// 不带 id 的请求也无法回到对端,但仍要返一帧,id 填空串
		return stdioResponse{Error: fmt.Sprintf("invalid envelope: %v", err)}
	}
	if env.Cap == "" {
		return stdioResponse{ID: env.ID, Error: "missing cap"}
	}

	switch env.Cap {
	case "create_issue":
		return env.createIssue()
	default:
		return stdioResponse{ID: env.ID, Error: fmt.Sprintf("unknown cap: %s", env.Cap)}
	}
}

// createIssue 解析 params 后返回一个"伪 GitLab issue"结果。
//
// 入参 shape(来自 plugin.json schema):
//
//	{
//	  "project_id": "42",
//	  "title":      "...",
//	  "body":       "..."
//	}
//
// 出参 shape(对齐 GitLab REST API POST /projects/:id/issues 字段子集):
//
//	{
//	  "issue_url":  "https://gitlab.example.com/group/project/-/issues/42",
//	  "issue_iid":  42,
//	  "project_id":  "42",
//	  "title":       "...",
//	  "created_at":  "RFC3339",
//	  "stub":        true  ← 标识这是 demo 不真调 API
//	}
func (e stdioEnvelope) createIssue() stdioResponse {
	var params struct {
		ProjectID string `json:"project_id"`
		Title     string `json:"title"`
		Body      string `json:"body"`
	}
	if err := json.Unmarshal(e.Params, &params); err != nil {
		return stdioResponse{ID: e.ID, Error: fmt.Sprintf("invalid params: %v", err)}
	}
	if strings.TrimSpace(params.ProjectID) == "" {
		return stdioResponse{ID: e.ID, Error: "project_id is required"}
	}
	if strings.TrimSpace(params.Title) == "" {
		return stdioResponse{ID: e.ID, Error: "title is required"}
	}

	// 真实接 gitlab 时:这里 POST https://gitlab.com/api/v4/projects/:id/issues,
	// header 用 PLUGIN_TRACE_ID 做 trace 关联,token 走 vault.get_ref。
	// 当前 demo 用"已创建"作为占位,issue_iid 用时间戳伪生成。
	stub := struct {
		IssueURL  string `json:"issue_url"`
		IssueIID  int64  `json:"issue_iid"`
		ProjectID string `json:"project_id"`
		Title     string `json:"title"`
		Body      string `json:"body,omitempty"`
		CreatedAt string `json:"created_at"`
		Stub      bool   `json:"stub"`
	}{
		IssueURL:  fmt.Sprintf("https://gitlab.example.com/demo/project/-/issues/%d", time.Now().Unix()%1000),
		IssueIID:  time.Now().Unix() % 1000,
		ProjectID: params.ProjectID,
		Title:     params.Title,
		Body:      params.Body,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Stub:      true,
	}
	result, _ := json.Marshal(stub)
	return stdioResponse{ID: e.ID, Result: result}
}

// writeFrame 写一帧 stdio 响应,以 \n 结尾。
//
// B 侧 subprocess.go 用 io.ReadAll 一次性读 stdout,所以这里要 flush;
func writeFrame(w *bufio.Writer, frame stdioResponse) error {
	b, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := w.Write(b); err != nil {
		return err
	}
	return w.Flush()
}

// bytesTrim 去掉首尾 ASCII whitespace(包含 \r \n)。不加 unicode.IsSpace 是为
// 了 stub 阶段不出现 unicode 边界 case;真生产化时再换 strings.TrimSpace。
func bytesTrim(b []byte) []byte {
	start, end := 0, len(b)
	for start < end {
		c := b[start]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		start++
	}
	for end > start {
		c := b[end-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		end--
	}
	return b[start:end]
}

// runSelfTest 在 demo plugin 内部验证 envelope in/out 闭环。
//
// 不依赖 pluginhost server,直接在 subprocess 自我驱动;用管道喂入 envelope,
// 读 stdout,断言结果与期望一致。
//
// 这是为 P1 demo 准备的 CI-friendly 验证手段;真实部署期,pluginhost 会拉这个
// binary 一次,跑 5 条请求,5 条全部成功即代表 OK。
func runSelfTest() {
	req := stdioEnvelope{
		ID:     "self-test-1",
		Cap:    "create_issue",
		Params: json.RawMessage(`{"project_id":"42","title":"test","body":"self-test"}`),
	}
	in, _ := json.Marshal(req)
	resp := handleRequest(in)

	if resp.ID != req.ID {
		failf("self-test failed: id mismatch (got %q want %q)", resp.ID, req.ID)
	}
	if resp.Error != "" {
		failf("self-test failed: error=%q", resp.Error)
	}
	var got struct {
		IssueURL  string `json:"issue_url"`
		IssueIID  int64  `json:"issue_iid"`
		ProjectID string `json:"project_id"`
		Title     string `json:"title"`
		Stub      bool   `json:"stub"`
	}
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		failf("self-test failed: unmarshal result: %v", err)
	}
	if !got.Stub {
		failf("self-test failed: stub flag missing")
	}
	if got.ProjectID != "42" || got.Title != "test" {
		failf("self-test failed: params roundtrip mismatch (%+v)", got)
	}
	fmt.Println("self-test OK")
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
