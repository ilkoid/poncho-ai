package main

import "math"

// SubjectCharProfile holds the derived characteristic profile for a subject.
// Built from local data frequency analysis — no API calls needed.
type SubjectCharProfile struct {
	ExpectedIDs   map[int]bool
	TotalExpected int
	CommonIDs     map[int]bool
	TotalCommon   int
	MaxChars      int
}

// CardMetric holds individual criterion values for a single card.
type CardMetric struct {
	NmID        int
	SubjectName string
	Year        string

	// Content
	TitleLen   int
	DescLen    int
	PhotoCount int
	HasVideo   bool
	HasBrand   bool

	// Characteristics
	HasProfile   bool
	ExpectedPct  float64 // % of expected (>=50%) chars filled
	CommonPct    float64 // % of common (>=90%) chars filled
	DensityPct   float64 // chars / max chars in subject
	CharCount    int

	// Technical
	DimHasValues bool
	SizeCount int

	// Market
	HasFeedbacks  bool
	AvgRating     float64
	FeedbackCount int
	AnswerRate    float64
}

// MeasureCard computes individual criterion values for a card.
func MeasureCard(card CardData, profile *SubjectCharProfile, fb *FeedbackStats) CardMetric {
	m := CardMetric{
		NmID:        card.NmID,
		SubjectName: card.SubjectName,
		Year:        card.Year,
		TitleLen:    len(card.Title),
		DescLen:     len(card.Description),
		PhotoCount:  card.PhotoCount,
		HasVideo:    card.Video != "",
		HasBrand:    card.Brand != "",
		DimHasValues: card.DimLength > 0 || card.DimWidth > 0 || card.DimHeight > 0,
		SizeCount:   card.SizeCount,
		CharCount:   len(card.CharIDs),
	}

	if profile != nil && profile.TotalExpected > 0 {
		m.HasProfile = true
		filled := 0
		for id := range card.CharIDs {
			if profile.ExpectedIDs[id] {
				filled++
			}
		}
		m.ExpectedPct = float64(filled) / float64(profile.TotalExpected) * 100

		if profile.TotalCommon > 0 {
			cf := 0
			for id := range card.CharIDs {
				if profile.CommonIDs[id] {
					cf++
				}
			}
			m.CommonPct = float64(cf) / float64(profile.TotalCommon) * 100
		}

		if profile.MaxChars > 0 {
			m.DensityPct = math.Min(100, float64(len(card.CharIDs))/float64(profile.MaxChars)*100)
		}
	}

	if fb != nil && fb.HasFeedbacks {
		m.HasFeedbacks = true
		m.AvgRating = fb.AvgRating
		m.FeedbackCount = fb.FeedbackCount
		m.AnswerRate = fb.AnswerRate
	}

	return m
}

// BuildSubjectCharProfiles builds per-subject characteristic profiles from frequency data.
func BuildSubjectCharProfiles(freqs []CharFrequency) map[int]*SubjectCharProfile {
	profiles := make(map[int]*SubjectCharProfile)
	for _, f := range freqs {
		p, ok := profiles[f.SubjectID]
		if !ok {
			p = &SubjectCharProfile{
				ExpectedIDs: make(map[int]bool),
				CommonIDs:   make(map[int]bool),
			}
			profiles[f.SubjectID] = p
		}
		if f.Frequency >= 0.50 {
			p.ExpectedIDs[f.CharID] = true
			p.TotalExpected++
		}
		if f.Frequency >= 0.90 {
			p.CommonIDs[f.CharID] = true
			p.TotalCommon++
		}
	}
	return profiles
}

// ComputeSubjectMaxChars computes max characteristic count per subject from loaded cards.
func ComputeSubjectMaxChars(cards []CardData) map[int]int {
	maxMap := make(map[int]int)
	for _, c := range cards {
		cnt := len(c.CharIDs)
		if cnt > maxMap[c.SubjectID] {
			maxMap[c.SubjectID] = cnt
		}
	}
	return maxMap
}
