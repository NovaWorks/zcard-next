// Package mcysign implements mcy-shop Kernel\Util\Str::generateSignature for
// scalar POST fields. Array fields must be rejected by the protocol handler.
package mcysign

import (
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
)

var escapedByte = regexp.MustCompile(`%[0-9a-fA-F]{2}`)

func Sign(params map[string]string, secret string) string {
	values := url.Values{}
	for key, value := range params {
		if key != "sign" && value != "" {
			values.Set(key, value)
		}
	}
	// PHP appends the secret BEFORE urldecode. Decode valid escapes even when
	// another percent sequence is malformed, matching PHP's permissive behavior.
	message := strings.ReplaceAll(values.Encode()+"&key="+secret, "+", " ")
	message = escapedByte.ReplaceAllStringFunc(message, func(s string) string {
		decoded, _ := url.PathUnescape(s)
		return decoded
	})
	sum := md5.Sum([]byte(message))
	return hex.EncodeToString(sum[:])
}

func Verify(params map[string]string, secret, signature string) bool {
	return subtle.ConstantTimeCompare([]byte(Sign(params, secret)), []byte(signature)) == 1
}
