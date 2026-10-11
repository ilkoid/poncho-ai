package apispecs

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
)

// DefaultUserAgent — браузерный UA: витринные/портальные эндпоинты режут
// не-браузерные клиентские строки (curl/Go) на WAF-уровне.
const DefaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// ErrChallenge — ответ пришёл от антибот/WAF-слоя, а не от сервиса.
// Повторные HTTP-попытки обычно только продлевают карантин IP, поэтому
// вызывающий код должен переключиться на CDP-канал, а не ретраить.
var ErrChallenge = errors.New("apispecs: antibot challenge or WAF block")

// challengeMarkers — фрагменты страниц-заглушек разных антиботов.
var challengeMarkers = [][]byte{
	[]byte("__wbaas"),                    // статика антибота WB
	[]byte("Подозрительная активность"),  // страница карантина WB
	[]byte("captcha-support@rwb.ru"),     // WB support link
	[]byte("Just a moment"),              // Cloudflare
	[]byte("challenge-platform"),         // Cloudflare
}

// LooksLikeChallenge классифицирует тело ответа как страницу антибота.
// Спеки — всегда YAML/JSON, поэтому любой text/html для этих эндпоинтов
// означает, что ответ от ворот, а не от сервиса.
func LooksLikeChallenge(contentType string, body []byte) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "text/html") {
		return true
	}
	for _, m := range challengeMarkers {
		if bytes.Contains(body, m) {
			return true
		}
	}
	return false
}

// challengeStatus — статусы, по которым одним кодом видно ворота.
// 498 — нестандартный код "invalid token", который использует WAF WB.
func challengeStatus(code int) bool {
	switch code {
	case http.StatusForbidden, 498, http.StatusServiceUnavailable:
		return true
	}
	return false
}
