package deepseek

import (
	"encoding/json"
	"reflect"
	"testing"

	"example.com/agent-harness/internal/agent"
)

func TestEncodeMessagesJSON(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.RoleSystem, Content: "Follow the migration specification."},
		{Role: agent.RoleUser, Content: "Read the repository."},
		{
			Role: agent.RoleAssistant, Content: "Inspecting two files.",
			ToolCalls: []agent.ToolCall{
				{ID: "call-1", Name: "read_file", Arguments: `{"root":"workspace","path":"go.mod"}`},
				{ID: "call-2", Name: "read_file", Arguments: "not JSON"},
			},
		},
		{Role: agent.RoleTool, Content: "module example.com/demo-orders", ToolCallID: "call-1"},
		{Role: agent.RoleTool, Content: "arguments must be a JSON object", ToolCallID: "call-2", IsError: true},
		{Role: agent.RoleAssistant},
	}
	// 使用协议 JSON 检查字段命名、嵌套和省略规则，不依赖对象字段顺序。
	wantJSON := `[
		{"role":"system","content":"Follow the migration specification."},
		{"role":"user","content":"Read the repository."},
		{"role":"assistant","content":"Inspecting two files.","tool_calls":[
			{"id":"call-1","type":"function","function":{"name":"read_file","arguments":"{\"root\":\"workspace\",\"path\":\"go.mod\"}"}},
			{"id":"call-2","type":"function","function":{"name":"read_file","arguments":"not JSON"}}
		]},
		{"role":"tool","content":"module example.com/demo-orders","tool_call_id":"call-1"},
		{"role":"tool","content":"Tool error: arguments must be a JSON object","tool_call_id":"call-2"},
		{"role":"assistant","content":""}
	]`
	data, err := json.Marshal(encodeMessages(messages))
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected protocol JSON:\n%s", data)
	}
}

func TestEncodeMessagesDoesNotModifyOrAliasInput(t *testing.T) {
	messages := []agent.Message{
		{
			Role:      agent.RoleAssistant,
			ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "read_file", Arguments: "{}"}},
		},
		{Role: agent.RoleTool, Content: "file not found", ToolCallID: "call-1", IsError: true},
	}
	before, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	encoded := encodeMessages(messages)
	if encoded[1].Content != "Tool error: file not found" || messages[1].Content != "file not found" {
		t.Fatal("error feedback was not converted independently")
	}
	encoded[0].ToolCalls[0].ID = "changed"
	encoded[0].ToolCalls[0].Function.Arguments = "changed"
	encoded[1].Content = "changed"
	after, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("protocol conversion modified caller-owned messages")
	}
}

func TestEncodeToolsJSON(t *testing.T) {
	tools := []agent.ToolSpec{
		{
			Name: "read_file", Description: "Read a text file.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{"path":{"type":"string"}},
				"required":["path"],"additionalProperties":false
			}`),
		},
		{
			Name: "go_check", Description: "Build or test the workspace.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{"kind":{"type":"string","enum":["build","test"]}},
				"required":["kind"],"additionalProperties":false
			}`),
		},
	}
	before, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(encodeTools(tools))
	if err != nil {
		t.Fatal(err)
	}
	// 对照协议 JSON，确保参数规则是嵌套对象，而非字符串或字节数组。
	wantJSON := `[
		{"type":"function","function":{
			"name":"read_file","description":"Read a text file.",
			"parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}
		}},
		{"type":"function","function":{
			"name":"go_check","description":"Build or test the workspace.",
			"parameters":{"type":"object","properties":{"kind":{"type":"string","enum":["build","test"]}},"required":["kind"],"additionalProperties":false}
		}}
	]`
	var got, want any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected tool definitions:\n%s", data)
	}
	after, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("tool conversion modified caller-owned definitions")
	}
}

func TestDecodeReply(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		finish    agent.FinishReason
		calls     int
		wantError bool
	}{
		{"text", `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`, agent.FinishStop, 0, false},
		{"null-text-and-multiple-calls", `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"a","type":"function","function":{"name":"read_file","arguments":"not JSON"}},{"id":"b","type":"function","function":{"name":"search","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, agent.FinishToolCalls, 2, false},
		{"truncated-call", `{"choices":[{"message":{"role":"assistant","content":"partial","tool_calls":[{"function":{"arguments":"{"}}]},"finish_reason":"length"}]}`, agent.FinishLength, 1, false},
		{"empty-choices", `{"choices":[]}`, "", 0, true},
		{"multiple-choices", `{"choices":[{},{}]}`, "", 0, true},
		{"missing-message", `{"choices":[{"finish_reason":"stop"}]}`, "", 0, true},
		{"wrong-role", `{"choices":[{"message":{"role":"user"},"finish_reason":"stop"}]}`, "", 0, true},
		{"unknown-finish", `{"choices":[{"message":{"role":"assistant"},"finish_reason":"content_filter"}]}`, "", 0, true},
		{"wrong-tool-type", `{"choices":[{"message":{"role":"assistant","tool_calls":[{"type":"custom"}]},"finish_reason":"tool_calls"}]}`, "", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response chatResponse
			if err := json.Unmarshal([]byte(tc.body), &response); err != nil {
				t.Fatal(err)
			}
			reply, err := decodeReply(response)
			if (err != nil) != tc.wantError {
				t.Fatalf("reply=%+v error=%v", reply, err)
			}
			if tc.wantError {
				return
			}
			if reply.Finish != tc.finish || len(reply.ToolCalls) != tc.calls || reply.Usage != nil {
				t.Fatalf("unexpected reply: %+v", reply)
			}
			if tc.name == "null-text-and-multiple-calls" && (reply.Content != "" || reply.ToolCalls[0].ID != "a" || reply.ToolCalls[0].Arguments != "not JSON" || reply.ToolCalls[1].ID != "b") {
				t.Fatalf("tool calls were changed: %+v", reply)
			}
		})
	}
}

func TestDecodeReplyRetainsUsageOnProtocolError(t *testing.T) {
	var response chatResponse
	if err := json.Unmarshal([]byte(`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_cache_hit_tokens":0}}`), &response); err != nil {
		t.Fatal(err)
	}
	reply, err := decodeReply(response)
	if err == nil || reply.Usage == nil || reply.Usage.InputTokens != 10 || reply.Usage.OutputTokens != 2 || reply.Usage.TotalTokens != 12 || reply.Usage.CacheHitTokens == nil || *reply.Usage.CacheHitTokens != 0 || reply.Usage.CacheMissTokens != nil {
		t.Fatalf("usage lost or invented: %+v %v", reply.Usage, err)
	}
}
