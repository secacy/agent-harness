package webapi

// translateRequest 保持原翻译边界的三个输入字段。
type translateRequest struct{ Text, Source, Destination string }
type translateResponse struct{ Text string }
