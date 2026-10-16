package itemrating

import (
	"context"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WBSource is the real-API Source: thin adapter over the typed wb.Client method
// (Rule 11: no direct client.Post from the domain package).
type WBSource struct {
	client    *wb.Client
	rateLimit int // req/min, stored here and passed to the client method
	burst     int
}

// NewWBSource creates a real-API source.
func NewWBSource(client *wb.Client, rateLimit, burst int) *WBSource {
	return &WBSource{client: client, rateLimit: rateLimit, burst: burst}
}

// FetchPage fetches one page and converts WB types to domain rows.
// Raw JSON bytes travel untouched from the API structs to the rows.
func (s *WBSource) FetchPage(ctx context.Context, req wb.ItemRatingRequest) (Page, error) {
	resp, err := s.client.GetItemRating(ctx, req, s.rateLimit, s.burst)
	if err != nil {
		return Page{}, fmt.Errorf("item rating page (offset=%d): %w", req.Offset, err)
	}

	page := Page{
		Items:    make([]Row, 0, len(resp.Data.Items)),
		APICount: len(resp.Data.Items),
		Seller:   convertSeller(resp, req),
	}
	for _, it := range resp.Data.Items {
		if it.NmID <= 0 {
			continue // invalid row guard
		}
		page.Items = append(page.Items, convertItem(it, req.CurrentPeriod.Start, req.CurrentPeriod.End))
	}
	return page, nil
}

// convertItem maps one WB item to a domain row. Period comes from the request
// because the API response items do not repeat it. Raw JSON travels untouched.
func convertItem(it wb.ItemRatingItem, periodStart, periodEnd string) Row {
	return Row{
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,

		NmID:        it.NmID,
		VendorCode:  it.VendorCode,
		Title:       it.Title,
		SubjectID:   it.SubjectID,
		SubjectName: it.SubjectName,
		BrandName:   it.BrandName,
		TagID:       it.TagID,
		TagName:     it.TagName,

		PinnedFeedback: it.PinnedFeedback,
		CardRating:     it.Rating,

		FeedbackRatingCurrent:    it.FeedbackRating.Current,
		FeedbackRatingDynamics:   it.FeedbackRating.Dynamics,
		FeedbackRatingPercentile: it.FeedbackRating.Percentile,

		FeedbackCountInc:      it.FeedbackCount.Current,
		FeedbackCountDynamics: it.FeedbackCount.Dynamics,

		FiveStarInc:       it.FiveStar.Current,
		FiveStarDynamics:  it.FiveStar.Dynamics,
		FourStarInc:       it.FourStar.Current,
		FourStarDynamics:  it.FourStar.Dynamics,
		ThreeStarInc:      it.ThreeStar.Current,
		ThreeStarDynamics: it.ThreeStar.Dynamics,
		TwoStarInc:        it.TwoStar.Current,
		TwoStarDynamics:   it.TwoStar.Dynamics,
		OneStarInc:        it.OneStar.Current,
		OneStarDynamics:   it.OneStar.Dynamics,

		Disqualified: it.Disqualified,
		IsShadowed:   it.IsShadowed,

		RawItem: it.Raw,
	}
}

// convertSeller maps the account-level blocks to a SellerRow.
func convertSeller(resp *wb.ItemRatingResponse, req wb.ItemRatingRequest) *SellerRow {
	fi := resp.Data.FeedbackIncrease
	return &SellerRow{
		PeriodStart: req.CurrentPeriod.Start,
		PeriodEnd:   req.CurrentPeriod.End,

		SellerRatingCurrent:  resp.Data.SellerRating.Current,
		SellerRatingDynamics: resp.Data.SellerRating.Dynamics,

		FbIncreaseCurrent:  fi.Current,
		FbIncreaseTotal:    fi.Total,
		FbIncreaseDynamics: fi.Dynamics,

		FiveStarInc:        fi.FiveStar.Current,
		FiveStarDynamics:   fi.FiveStar.Dynamics,
		FiveStarTotal:      fi.FiveStar.Total,
		FourStarInc:        fi.FourStar.Current,
		FourStarDynamics:   fi.FourStar.Dynamics,
		FourStarTotal:      fi.FourStar.Total,
		ThreeStarInc:       fi.ThreeStar.Current,
		ThreeStarDynamics:  fi.ThreeStar.Dynamics,
		ThreeStarTotal:     fi.ThreeStar.Total,
		TwoStarInc:         fi.TwoStar.Current,
		TwoStarDynamics:    fi.TwoStar.Dynamics,
		TwoStarTotal:       fi.TwoStar.Total,
		OneStarInc:         fi.OneStar.Current,
		OneStarDynamics:    fi.OneStar.Dynamics,
		OneStarTotal:       fi.OneStar.Total,

		RawSeller:     resp.Data.SellerRating.Raw,
		RawFbIncrease: fi.Raw,
	}
}
