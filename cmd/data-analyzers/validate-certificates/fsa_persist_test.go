package main

import (
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/fsa"
)

var testCheckedAt = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func TestDocKindOf(t *testing.T) {
	cases := []struct {
		certType string
		want     string
	}{
		{"Декларация о соответствии ТР ЕАЭС", fsa.DocKindDeclaration},
		{"Сертификат соответствия ТР ЕАЭС", fsa.DocKindCertificate},
		{"", fsa.DocKindCertificate},
	}
	for _, tc := range cases {
		if got := docKindOf(tc.certType); got != tc.want {
			t.Errorf("docKindOf(%q) = %q, want %q", tc.certType, got, tc.want)
		}
	}
}

func TestCheckResultFromFSAFound(t *testing.T) {
	res := &FSASearchResult{
		ID:         3254311,
		Number:     "ЕАЭС RU С-CN.ПФ02.В.07011/23",
		RegDate:    "2023-05-25",
		EndDate:    "24.05.2026",
		StatusID:   6,
		CertType:   "Сертификат соответствия требованиям ТР ЕАЭС",
		ObjectType: "Серийный выпуск",
	}

	cr := checkResultFromFSA(res.Number, "Сертификат соответствия", res, true, testCheckedAt)

	if !cr.Found || !cr.ExactMatch {
		t.Errorf("Found/ExactMatch = %v/%v, want true/true", cr.Found, cr.ExactMatch)
	}
	if cr.FsaID == nil || *cr.FsaID != 3254311 {
		t.Errorf("FsaID = %v, want *3254311", cr.FsaID)
	}
	if cr.IDStatus == nil || *cr.IDStatus != 6 {
		t.Errorf("IDStatus = %v, want *6", cr.IDStatus)
	}
	if cr.EndDate != "24.05.2026" || cr.RegDate != "2023-05-25" {
		t.Errorf("даты должны храниться как вернул API: got %q / %q", cr.RegDate, cr.EndDate)
	}
	if cr.CertType == "" || cr.ObjectType == "" {
		t.Errorf("CertType/ObjectType не перенесены")
	}
	if cr.Error != "" || cr.SkippedReason != "" {
		t.Errorf("Error/SkippedReason должны быть пустыми для успешного поиска")
	}
	if cr.DocKind != fsa.DocKindCertificate {
		t.Errorf("DocKind = %q, want certificate", cr.DocKind)
	}
}

func TestCheckResultFromFSANotFound(t *testing.T) {
	cr := checkResultFromFSA("ЕАЭС RU С-CN.AG01.В.00001/24", "Сертификат соответствия", nil, false, testCheckedAt)

	if cr.Found {
		t.Errorf("Found = true, want false")
	}
	if cr.FsaID != nil || cr.IDStatus != nil {
		t.Errorf("FsaID/IDStatus должны быть NULL для не найденного номера")
	}
	if cr.EndDate != "" || cr.RegDate != "" {
		t.Errorf("даты должны быть пустыми для не найденного номера")
	}
}

func TestCheckResultFromFSADeclarationApprox(t *testing.T) {
	res := &FSASearchResult{ID: 42, Number: "другой номер", StatusID: 1, EndDate: "2024-12-26"}

	cr := checkResultFromFSA("ЕАЭС N RU Д-CN.АГ01.В.20001/23", "Декларация о соответствии", res, false, testCheckedAt)

	if cr.DocKind != fsa.DocKindDeclaration {
		t.Errorf("DocKind = %q, want declaration", cr.DocKind)
	}
	if cr.ExactMatch {
		t.Errorf("ExactMatch = true, want false (approx)")
	}
	if cr.FsaID == nil || *cr.FsaID != 42 {
		t.Errorf("FsaID должен переноситься даже при approx-матче")
	}
}

func TestSkippedCheck(t *testing.T) {
	cr := skippedCheck("ЕАЭС KG417/09772", "Сертификат соответствия", testCheckedAt)

	if cr.SkippedReason == "" {
		t.Errorf("SkippedReason пуст")
	}
	if cr.Found || cr.FsaID != nil || cr.IDStatus != nil {
		t.Errorf("пропущенный номер: Found/FsaID/IDStatus должны быть false/NULL")
	}
	if cr.Error != "" {
		t.Errorf("Error должен быть пустым для skip (это не ошибка)")
	}
}

func TestErrorCheck(t *testing.T) {
	cr := errorCheck("ЕАЭС RU С-CN.ПФ02.В.07011/23", "Сертификат соответствия", "FSA API 503: ...", testCheckedAt)

	if cr.Error != "FSA API 503: ..." {
		t.Errorf("Error = %q", cr.Error)
	}
	if cr.Found || cr.FsaID != nil || cr.SkippedReason != "" {
		t.Errorf("ошибка: Found/FsaID/SkippedReason должны быть false/NULL/пусто")
	}
}
