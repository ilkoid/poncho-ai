package campaigns

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ToolID constants for SetRateLimit — must match the ToolIDs used in wb.Client calls.
// CLI must call wbClient.SetRateLimit() for each before using the source.
const (
	ToolIDPromotionCount = "get_promotion_count"   // 300/min, burst 5
	ToolIDAdvertDetails  = "get_advert_details"    // 300/min, burst 5
	ToolIDFullstats      = "get_campaign_fullstats" // 3/min, burst 1
)

// WBSource adapts *wb.Client to CampaignsSource interface.
// *wb.Client already has the 3 required methods with matching signatures,
// so WBSource delegates directly — no adapter boilerplate needed.
type WBSource struct {
	client *wb.Client
}

// NewWBSource creates a CampaignsSource backed by the real WB Promotion API.
func NewWBSource(client *wb.Client) *WBSource {
	return &WBSource{client: client}
}

// GetCampaignList delegates to wb.Client.GetPromotionCount.
func (s *WBSource) GetCampaignList(ctx context.Context) (*wb.PromotionCountResponse, error) {
	return s.client.GetPromotionCount(ctx)
}

// GetAdvertDetails delegates to wb.Client.GetAdvertDetails.
func (s *WBSource) GetAdvertDetails(ctx context.Context, ids []int) ([]wb.AdvertDetail, error) {
	return s.client.GetAdvertDetails(ctx, ids)
}

// GetAllAdvertDetails delegates to wb.Client.GetAdvertDetails with empty IDs (no filter).
func (s *WBSource) GetAllAdvertDetails(ctx context.Context) ([]wb.AdvertDetail, error) {
	return s.client.GetAdvertDetails(ctx, nil)
}

// GetCampaignFullstats delegates to wb.Client.GetCampaignFullstats.
func (s *WBSource) GetCampaignFullstats(ctx context.Context, ids []int, begin, end string, rl, burst int) ([]wb.CampaignFullstatsResponse, error) {
	return s.client.GetCampaignFullstats(ctx, ids, begin, end, rl, burst)
}
