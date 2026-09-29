// Package shipping contains local address metadata and shipping validation.
package shipping

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

//go:embed regions.json
var RegionsJSON []byte
var Regions map[string]map[string]string

func init() {
	if err := json.Unmarshal(RegionsJSON, &Regions); err != nil {
		panic(err)
	}
}
func Normalize(a map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range []string{"country", "region", "city", "district", "address", "name", "phone", "postal_code"} {
		v := strings.TrimSpace(a[k])
		if utf8.RuneCountInString(v) > 300 || strings.ContainsAny(v, "\x00\r\n") {
			return nil, fmt.Errorf("收货信息格式无效")
		}
		out[k] = v
	}
	out["country"] = strings.ToUpper(out["country"])
	return out, nil
}
func Validate(a map[string]string) (map[string]string, error) {
	out, e := Normalize(a)
	if e != nil {
		return nil, e
	}
	r, ok := Regions[out["country"]]
	if !ok {
		return nil, fmt.Errorf("请选择有效国家或地区")
	}
	required := r["require"]
	if required == "" {
		required = "AC"
	}
	for _, k := range []string{"name", "phone", "address"} {
		if out[k] == "" {
			return nil, fmt.Errorf("请填写收货人、电话号码和详细地址")
		}
	}
	for code, key := range map[string]string{"S": "region", "C": "city", "Z": "postal_code"} {
		if strings.Contains(required, code) && out[key] == "" {
			return nil, fmt.Errorf("请完整填写国家要求的地区、城市和邮编")
		}
	}
	if keys := r["sub_keys"]; keys != "" {
		valid := false
		for _, k := range strings.Split(keys, "~") {
			if k == out["region"] {
				valid = true
			}
		}
		if !valid {
			return nil, fmt.Errorf("请选择属于该国家的地区")
		}
	}
	digits := 0
	for _, c := range out["phone"] {
		if c >= '0' && c <= '9' {
			digits++
		}
	}
	if digits < 6 || digits > 15 || !regexp.MustCompile(`^\+?[0-9 ()\-]{6,30}$`).MatchString(out["phone"]) {
		return nil, fmt.Errorf("电话号码格式无效，请包含国际区号")
	}
	if pattern := r["zip"]; pattern != "" && out["postal_code"] != "" {
		if re, e := regexp.Compile("^(?:" + pattern + ")$"); e == nil && !re.MatchString(out["postal_code"]) {
			return nil, fmt.Errorf("邮编与国家格式不符")
		}
	}
	return out, nil
}
