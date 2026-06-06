package core

// ApiResponse is a generic wrapper for successful API responses.
type ApiResponse[T any] struct {
	Success bool `json:"success"`
	Data    T    `json:"data"`
}

// ErrorResponse is the standard structure for error API responses.
type ErrorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// PaginatedResponse is a generic wrapper for paginated list responses.
type PaginatedResponse[T any] struct {
	Success    bool  `json:"success"`
	Data       []T   `json:"data"`
	TotalCount int64 `json:"totalCount"`
	Page       int   `json:"page"`
	PageSize   int   `json:"pageSize"`
}

// Success creates a successful ApiResponse with the given data.
func Success[T any](data T) ApiResponse[T] {
	return ApiResponse[T]{Success: true, Data: data}
}

// Error creates an ErrorResponse with the given message.
func Error(err string) ErrorResponse {
	return ErrorResponse{Success: false, Error: err}
}

// NewPaginated creates a PaginatedResponse.
func NewPaginated[T any](data []T, totalCount int64, page, pageSize int) PaginatedResponse[T] {
	return PaginatedResponse[T]{
		Success:    true,
		Data:       data,
		TotalCount: totalCount,
		Page:       page,
		PageSize:   pageSize,
	}
}
