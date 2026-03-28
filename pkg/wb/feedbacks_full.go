// Package wb provides types for WB API interaction.
//
// This file contains "Full" types for the Feedbacks API with flattened fields
// for direct SQLite INSERT without transformation. These differ from the
// simplified Feedback/Question types used by tools — those use nested structs.
//
// Source: docs/09-communications.yaml API schema.
package wb

import (
	"encoding/json"
)

// ============================================================================
// FeedbackFull — full feedback from API (39 fields)
//
// Schema: docs/09-communications.yaml → responseFeedback
// Nested objects (answer, productDetails, video) are flattened into prefixed fields
// for direct SQLite INSERT without transformation.
// ============================================================================
type FeedbackFull struct {
	// Top-level fields
	ID                              string  `json:"id"`
	Text                            string  `json:"text"`
	Pros                            string  `json:"pros"`
	Cons                            string  `json:"cons"`
	ProductValuation                int     `json:"productValuation"`
	CreatedDate                    string  `json:"createdDate"`
	State                           string  `json:"state"`
	UserName                       string  `json:"userName"`
	WasViewed                      bool    `json:"wasViewed"`
	OrderStatus                    string  `json:"orderStatus"`
	MatchingSize                   string  `json:"matchingSize"`
	IsAbleSupplierFeedbackValuation bool   `json:"isAbleSupplierFeedbackValuation"`
	SupplierFeedbackValuation      int     `json:"supplierFeedbackValuation"`
	IsAbleSupplierProductValuation bool    `json:"isAbleSupplierProductValuation"`
	SupplierProductValuation       int     `json:"supplierProductValuation"`
	IsAbleReturnProductOrders      bool    `json:"isAbleReturnProductOrders"`
	ReturnProductOrdersDate        *string `json:"returnProductOrdersDate"`
	Bables                         []string `json:"bables"`
	LastOrderShkId                 int     `json:"lastOrderShkId"`
	LastOrderCreatedAt             string  `json:"lastOrderCreatedAt"`
	Color                          string  `json:"color"`
	SubjectId                      int     `json:"subjectId"`
	SubjectName                    string  `json:"subjectName"`
	ParentFeedbackId               *string `json:"parentFeedbackId"`
	ChildFeedbackId                *string `json:"childFeedbackId"`

	// answer.* — nested object flattened
	AnswerText    *string `json:"answerText"`
	AnswerState   *string `json:"answerState"`
	AnswerEditable *bool  `json:"answerEditable"`

	// photoLinks — array of objects stored as JSON TEXT
	PhotoLinksJSON *string `json:"photoLinksJson"`

	// video.* — nested object flattened
	VideoPreviewImage *string `json:"videoPreviewImage"`
	VideoLink         *string `json:"videoLink"`
	VideoDurationSec  *int    `json:"videoDurationSec"`

	// productDetails.* — nested object flattened
	ProductImtId     int     `json:"productImtId"`
	ProductNmId      int     `json:"productNmId"`
	ProductName      string  `json:"productName"`
	SupplierArticle  *string `json:"supplierArticle"`
	SupplierName     *string `json:"supplierName"`
	BrandName        *string `json:"brandName"`
	Size             string  `json:"size"`
}

// feedbackFullJSON — internal type for JSON unmarshal with nested objects.
type feedbackFullJSON struct {
	ID                              string          `json:"id"`
	Text                            string          `json:"text"`
	Pros                            string          `json:"pros"`
	Cons                            string          `json:"cons"`
	ProductValuation                int             `json:"productValuation"`
	CreatedDate                    string          `json:"createdDate"`
	State                           string          `json:"state"`
	UserName                       string          `json:"userName"`
	WasViewed                      bool            `json:"wasViewed"`
	OrderStatus                    string          `json:"orderStatus"`
	MatchingSize                   string          `json:"matchingSize"`
	IsAbleSupplierFeedbackValuation bool            `json:"isAbleSupplierFeedbackValuation"`
	SupplierFeedbackValuation      int             `json:"supplierFeedbackValuation"`
	IsAbleSupplierProductValuation bool            `json:"isAbleSupplierProductValuation"`
	SupplierProductValuation       int             `json:"supplierProductValuation"`
	IsAbleReturnProductOrders      bool            `json:"isAbleReturnProductOrders"`
	ReturnProductOrdersDate        *string         `json:"returnProductOrdersDate"`
	Bables                         []string        `json:"bables"`
	LastOrderShkId                 int             `json:"lastOrderShkId"`
	LastOrderCreatedAt             string          `json:"lastOrderCreatedAt"`
	Color                          string          `json:"color"`
	SubjectId                      int             `json:"subjectId"`
	SubjectName                    string          `json:"subjectName"`
	ParentFeedbackId               *string         `json:"parentFeedbackId"`
	ChildFeedbackId                *string         `json:"childFeedbackId"`
	Answer                         *feedbackFullAnswer `json:"answer"`
	PhotoLinks                     json.RawMessage `json:"photoLinks"`
	Video                          *feedbackFullVideo  `json:"video"`
	ProductDetails                 *feedbackFullProduct `json:"productDetails"`
}

type feedbackFullAnswer struct {
	Text     *string `json:"text"`
	State    *string `json:"state"`
	Editable *bool   `json:"editable"`
}

type feedbackFullVideo struct {
	PreviewImage *string `json:"previewImage"`
	Link         *string `json:"link"`
	DurationSec  *int    `json:"durationSec"`
}

type feedbackFullProduct struct {
	ImtId          int     `json:"imtId"`
	NmId           int     `json:"nmId"`
	ProductName    string  `json:"productName"`
	SupplierArticle *string `json:"supplierArticle"`
	SupplierName   *string `json:"supplierName"`
	BrandName      *string `json:"brandName"`
	Size           string  `json:"size"`
}

// UnmarshalJSON flattens nested JSON objects into prefixed fields.
func (f *FeedbackFull) UnmarshalJSON(data []byte) error {
	var raw feedbackFullJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	f.ID = raw.ID
	f.Text = raw.Text
	f.Pros = raw.Pros
	f.Cons = raw.Cons
	f.ProductValuation = raw.ProductValuation
	f.CreatedDate = raw.CreatedDate
	f.State = raw.State
	f.UserName = raw.UserName
	f.WasViewed = raw.WasViewed
	f.OrderStatus = raw.OrderStatus
	f.MatchingSize = raw.MatchingSize
	f.IsAbleSupplierFeedbackValuation = raw.IsAbleSupplierFeedbackValuation
	f.SupplierFeedbackValuation = raw.SupplierFeedbackValuation
	f.IsAbleSupplierProductValuation = raw.IsAbleSupplierProductValuation
	f.SupplierProductValuation = raw.SupplierProductValuation
	f.IsAbleReturnProductOrders = raw.IsAbleReturnProductOrders
	f.ReturnProductOrdersDate = raw.ReturnProductOrdersDate
	f.Bables = raw.Bables
	f.LastOrderShkId = raw.LastOrderShkId
	f.LastOrderCreatedAt = raw.LastOrderCreatedAt
	f.Color = raw.Color
	f.SubjectId = raw.SubjectId
	f.SubjectName = raw.SubjectName
	f.ParentFeedbackId = raw.ParentFeedbackId
	f.ChildFeedbackId = raw.ChildFeedbackId

	// Flatten answer
	if raw.Answer != nil {
		f.AnswerText = raw.Answer.Text
		f.AnswerState = raw.Answer.State
		f.AnswerEditable = raw.Answer.Editable
	}

	// Store photoLinks as JSON TEXT (may be null)
	if raw.PhotoLinks != nil && string(raw.PhotoLinks) != "null" {
		s := string(raw.PhotoLinks)
		f.PhotoLinksJSON = &s
	}

	// Flatten video
	if raw.Video != nil {
		f.VideoPreviewImage = raw.Video.PreviewImage
		f.VideoLink = raw.Video.Link
		f.VideoDurationSec = raw.Video.DurationSec
	}

	// Flatten productDetails
	if raw.ProductDetails != nil {
		f.ProductImtId = raw.ProductDetails.ImtId
		f.ProductNmId = raw.ProductDetails.NmId
		f.ProductName = raw.ProductDetails.ProductName
		f.SupplierArticle = raw.ProductDetails.SupplierArticle
		f.SupplierName = raw.ProductDetails.SupplierName
		f.BrandName = raw.ProductDetails.BrandName
		f.Size = raw.ProductDetails.Size
	}

	return nil
}

// ============================================================================
// QuestionFull — full question from API (16 fields)
//
// Schema: docs/09-communications.yaml → /api/v1/questions response
// ============================================================================
type QuestionFull struct {
	// Top-level fields
	ID          string  `json:"id"`
	Text        string  `json:"text"`
	CreatedDate string  `json:"createdDate"`
	State       string  `json:"state"`
	WasViewed   bool    `json:"wasViewed"`
	IsWarned    bool    `json:"isWarned"`

	// answer.* — nested object flattened
	AnswerText       *string `json:"answerText"`
	AnswerEditable   *bool   `json:"answerEditable"`
	AnswerCreateDate *string `json:"answerCreateDate"`

	// productDetails.* — nested object flattened
	ProductImtId    int    `json:"productImtId"`
	ProductNmId     int    `json:"productNmId"`
	ProductName     string `json:"productName"`
	SupplierArticle string `json:"supplierArticle"`
	SupplierName    string `json:"supplierName"`
	BrandName       string `json:"brandName"`
}

// questionFullJSON — internal type for JSON unmarshal with nested objects.
type questionFullJSON struct {
	ID             string               `json:"id"`
	Text           string               `json:"text"`
	CreatedDate    string               `json:"createdDate"`
	State          string               `json:"state"`
	WasViewed      bool                 `json:"wasViewed"`
	IsWarned       bool                 `json:"isWarned"`
	Answer         *questionFullAnswer  `json:"answer"`
	ProductDetails *questionFullProduct `json:"productDetails"`
}

type questionFullAnswer struct {
	Text       *string `json:"text"`
	Editable   *bool   `json:"editable"`
	CreateDate *string `json:"createDate"`
}

type questionFullProduct struct {
	ImtId           int    `json:"imtId"`
	NmId            int    `json:"nmId"`
	ProductName     string `json:"productName"`
	SupplierArticle string `json:"supplierArticle"`
	SupplierName    string `json:"supplierName"`
	BrandName       string `json:"brandName"`
}

// UnmarshalJSON flattens nested JSON objects into prefixed fields.
func (q *QuestionFull) UnmarshalJSON(data []byte) error {
	var raw questionFullJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	q.ID = raw.ID
	q.Text = raw.Text
	q.CreatedDate = raw.CreatedDate
	q.State = raw.State
	q.WasViewed = raw.WasViewed
	q.IsWarned = raw.IsWarned

	// Flatten answer
	if raw.Answer != nil {
		q.AnswerText = raw.Answer.Text
		q.AnswerEditable = raw.Answer.Editable
		q.AnswerCreateDate = raw.Answer.CreateDate
	}

	// Flatten productDetails
	if raw.ProductDetails != nil {
		q.ProductImtId = raw.ProductDetails.ImtId
		q.ProductNmId = raw.ProductDetails.NmId
		q.ProductName = raw.ProductDetails.ProductName
		q.SupplierArticle = raw.ProductDetails.SupplierArticle
		q.SupplierName = raw.ProductDetails.SupplierName
		q.BrandName = raw.ProductDetails.BrandName
	}

	return nil
}
