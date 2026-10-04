package dtos

import "time"

type RealtimeReadyModel struct {
	Ready bool `json:"ready"`
}

type RealtimePingModel struct {
	PingID string `json:"pingId"`
}

type RealtimePongModel struct {
	PingID     string    `json:"pingId"`
	ServerTime time.Time `json:"serverTime"`
}
