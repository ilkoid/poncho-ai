package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
)

// MockSearchVisibilityClient returns simulated data for testing.
type MockSearchVisibilityClient struct{}

func NewMockSearchVisibilityClient() *MockSearchVisibilityClient {
	return &MockSearchVisibilityClient{}
}

func (m *MockSearchVisibilityClient) Post(ctx context.Context, toolID, endpoint string, rateLimit, burst int, path string, body interface{}, result interface{}) error {
	switch path {
	case "/api/v2/search-report/report":
		return m.mockReport(result)
	case "/api/v2/search-report/product/search-texts":
		return m.mockSearchTexts(body, result)
	default:
		return fmt.Errorf("mock: unknown path %s", path)
	}
}

func (m *MockSearchVisibilityClient) mockReport(result interface{}) error {
	resp := map[string]interface{}{
		"data": map[string]interface{}{
			"positionInfo": map[string]interface{}{
				"average": map[string]interface{}{
					"current":   float64(20 + rand.Intn(80)),
					"dynamics":  float64(rand.Intn(40) - 20),
				},
				"median": map[string]interface{}{
					"current":   float64(15 + rand.Intn(60)),
					"dynamics":  float64(rand.Intn(30) - 15),
				},
				"chartItems": []interface{}{},
				"clusters": map[string]interface{}{
					"firstHundred":  map[string]interface{}{"current": 30, "dynamics": 5},
					"secondHundred": map[string]interface{}{"current": 10, "dynamics": -2},
					"below":         map[string]interface{}{"current": 5, "dynamics": -3},
				},
			},
			"visibilityInfo": map[string]interface{}{
				"visibility": map[string]interface{}{
					"current":   float64(10 + rand.Intn(40)),
					"dynamics":  float64(rand.Intn(20) - 10),
				},
				"openCard": map[string]interface{}{
					"current":   50 + rand.Intn(200),
					"dynamics":  float64(rand.Intn(30) - 15),
				},
				"byDay": []interface{}{},
			},
			"groups": []interface{}{},
		},
	}

	raw, _ := json.Marshal(resp)
	return json.Unmarshal(raw, result)
}

func (m *MockSearchVisibilityClient) mockSearchTexts(body interface{}, result interface{}) error {
	mockQueries := []string{
		"платье женское", "вечернее платье", "платье черное",
		"платье длинное", "платье летнее", "сарафан",
		"платье коктейльное", "платье офисное", "платье красное",
		"платье с рукавами",
	}

	// Extract nmIDs from request body for realistic batch simulation.
	var nmIDs []int
	if bodyMap, ok := body.(map[string]interface{}); ok {
		if ids, ok := bodyMap["nmIds"].([]int); ok {
			nmIDs = ids
		}
	}
	if len(nmIDs) == 0 {
		nmIDs = []int{211131895}
	}

	items := make([]map[string]interface{}, 0, len(nmIDs)*len(mockQueries))
	for _, nmID := range nmIDs {
		for i, q := range mockQueries {
			items = append(items, map[string]interface{}{
				"text":          q,
				"nmId":          nmID,
				"vendorCode":    fmt.Sprintf("mock%d", nmID%100),
				"brandName":     "MockBrand",
				"subjectName":   "Платья",
				"frequency":     map[string]interface{}{"current": 50 + i*10, "dynamics": float64(rand.Intn(30) - 15)},
				"weekFrequency": 100 + i*20,
				"avgPosition":   map[string]interface{}{"current": float64(i*5 + 1), "dynamics": float64(rand.Intn(10) - 5)},
				"medianPosition": map[string]interface{}{"current": float64(i*4 + 1), "dynamics": float64(rand.Intn(10) - 5)},
				"openCard":      map[string]interface{}{"current": 10 + i*5, "dynamics": float64(rand.Intn(20) - 10), "percentile": 50},
				"addToCart":     map[string]interface{}{"current": 5 + i*3, "dynamics": float64(rand.Intn(15) - 7), "percentile": 40},
				"orders":        map[string]interface{}{"current": 2 + i*2, "dynamics": float64(rand.Intn(10) - 5), "percentile": 30},
			})
		}
	}

	resp := map[string]interface{}{
		"data": map[string]interface{}{
			"items":    items,
			"currency": "RUB",
		},
	}

	raw, _ := json.Marshal(resp)
	return json.Unmarshal(raw, result)
}
