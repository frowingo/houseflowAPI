package dtos

type RegisterDeviceModel struct {
	Token    string `json:"token" validate:"required,max=4096"`
	Platform string `json:"platform" validate:"required,oneof=ios android"`
}

type DeleteDeviceModel struct {
	Token string `json:"token" validate:"required,max=4096"`
}

type NotificationResponseModel struct {
	Id           string      `json:"id"`
	Type         string      `json:"type"`
	HouseId      string      `json:"houseId"`
	SenderUserId string      `json:"senderUserId"`
	ChoreId      *string     `json:"choreId,omitempty"`
	Title        string      `json:"title"`
	Message      string      `json:"message"`
	CreatedAt    UTCDateTime `json:"createdAt"`
}

// NotificationPageModel lists notifications newest first. NextCursor is
// passed as before to load older ones; empty means there are no more.
type NotificationPageModel struct {
	Items      []NotificationResponseModel `json:"items"`
	NextCursor string                      `json:"nextCursor,omitempty"`
}
