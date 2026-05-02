package signedasset

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	imageAssetPathPrefix = "/p/img"
	defaultImageAssetTTL = 24 * time.Hour
)

var (
	secretOnce sync.Once
	secret     []byte
)

func ImageAssetURL(assetID int64, thumbKB int) string {
	return ImageAssetURLWithTTL(assetID, thumbKB, defaultImageAssetTTL)
}

func ImageAssetURLWithTTL(assetID int64, thumbKB int, ttl time.Duration) string {
	if assetID <= 0 {
		return ""
	}
	if ttl <= 0 {
		ttl = defaultImageAssetTTL
	}
	exp := time.Now().Add(ttl).Unix()
	sig := imageAssetSignature(assetID, exp, thumbKB)
	if thumbKB > 0 {
		return fmt.Sprintf("%s/%d?exp=%d&thumb_kb=%d&sig=%s", imageAssetPathPrefix, assetID, exp, thumbKB, sig)
	}
	return fmt.Sprintf("%s/%d?exp=%d&sig=%s", imageAssetPathPrefix, assetID, exp, sig)
}

func VerifyImageAssetURL(assetID int64, exp int64, thumbKB int, sig string, now time.Time) bool {
	if assetID <= 0 || exp <= 0 || strings.TrimSpace(sig) == "" {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	if now.Unix() > exp {
		return false
	}
	want := imageAssetSignature(assetID, exp, thumbKB)
	return hmac.Equal([]byte(want), []byte(sig))
}

func imageAssetSignature(assetID int64, exp int64, thumbKB int) string {
	mac := hmac.New(sha256.New, imageAssetSecret())
	mac.Write([]byte(strconv.FormatInt(assetID, 10)))
	mac.Write([]byte("|"))
	mac.Write([]byte(strconv.FormatInt(exp, 10)))
	mac.Write([]byte("|"))
	mac.Write([]byte(strconv.Itoa(thumbKB)))
	sum := mac.Sum(nil)
	return hex.EncodeToString(sum[:16])
}

func imageAssetSecret() []byte {
	secretOnce.Do(func() {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			panic(fmt.Sprintf("signedasset: generate image proxy secret: %v", err))
		}
	})
	return secret
}
