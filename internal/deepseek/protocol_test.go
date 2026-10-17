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
