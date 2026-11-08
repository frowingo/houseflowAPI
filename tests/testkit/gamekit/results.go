package gamekit

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/models/core"
)

func MustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func (fixture *RocketsGatewayFixture) ResultHTTP(t *testing.T, index int, sessionID, userID string) (int, houseRockets.HouseRocketsResultModel) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, fixture.URLs[index]+"/api/v1/game/"+sessionID+"/result", nil)
	if userID != "" {
		request.Header.Set("Authorization", "Bearer "+GenerateGatewayToken(t, fixture.JWT, userID))
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body core.ApiResponse[houseRockets.HouseRocketsResultModel]
	if response.StatusCode == 200 {
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Success {
			t.Fatal("unsuccessful result response")
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	return response.StatusCode, body.Data
}
