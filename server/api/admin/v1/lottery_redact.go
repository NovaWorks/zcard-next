package adminv1

import "fmt"

// Redact prevents the request logger from recording configured prize secrets.
func (x *LotteryActivity) Redact() string {
	if x == nil {
		return "lottery activity <nil>"
	}
	return fmt.Sprintf("lottery activity id=%d revision=%d prizes=%d", x.Id, x.Revision, len(x.Prizes))
}
