package model

type ErrorResponse struct {
	Error Error `json:"error"`
}

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}
