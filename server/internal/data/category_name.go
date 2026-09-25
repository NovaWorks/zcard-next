package data

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const CategoryNameMaxChars = 100

func ValidateCategoryName(name string) error {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > CategoryNameMaxChars {
		return fmt.Errorf("分类名称须为 1–100 个字符（中文和表情各按一个字符计算）")
	}
	return nil
}
