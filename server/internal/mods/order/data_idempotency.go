package order

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/pluginstorage"
)

type RequestFingerprinter struct{ key []byte }

func NewRequestFingerprinter(c *conf.Data, d *data.Data) (*RequestFingerprinter, error) {
	used, err := data.Client(context.Background(), d).Order.Query().Where(order.RequestFingerprintNEQ("")).Exist(context.Background())
	if err != nil {
		return nil, err
	}
	key, err := pluginstorage.EnsureKey(c.PluginDataDir, !used)
	if err != nil {
		return nil, err
	}
	return &RequestFingerprinter{key: key}, nil
}
func validatePurchaseItems(in CreateOrderInput) error {
	if len(in.Items) == 0 || len(in.Items) > 100 {
		return fmt.Errorf("order.INVALID_ITEMS")
	}
	seen := map[[2]uint64]bool{}
	for _, it := range in.Items {
		k := itemKey(it.ProductID, it.SkuID)
		if it.ProductID == 0 || it.Quantity < 1 || it.Quantity > 100000 || seen[k] {
			return fmt.Errorf("order.FORM_INVALID: 商品规格重复或数量无效")
		}
		seen[k] = true
	}
	return nil
}
func (uc *OrderUsecase) idempotency(in CreateOrderInput) (key, legacy, fingerprint string, err error) {
	if in.IdempotencyKey == "" {
		return
	}
	if len(in.IdempotencyKey) > 128 {
		return "", "", "", fmt.Errorf("order.IDEMPOTENCY_CONFLICT")
	}
	for _, ch := range []byte(in.IdempotencyKey) {
		if ch < 32 || ch > 126 {
			return "", "", "", fmt.Errorf("order.IDEMPOTENCY_CONFLICT")
		}
	}
	if uc.Fingerprinter == nil || len(uc.Fingerprinter.key) != 32 {
		return "", "", "", fmt.Errorf("PLUGIN_UNAVAILABLE: request fingerprint key missing")
	}
	namespace, _ := json.Marshal(struct {
		Version   int    `json:"version"`
		SubsiteID string `json:"subsiteId"`
		UserID    string `json:"userId"`
		Key       string `json:"key"`
	}{2, fmt.Sprint(in.SubsiteID), fmt.Sprint(in.UserID), in.IdempotencyKey})
	h := sha256.Sum256(namespace)
	key = "idem2-" + hex.EncodeToString(h[:])
	h = sha256.Sum256([]byte(in.IdempotencyKey))
	legacy = "idem-" + hex.EncodeToString(h[:])
	type item struct {
		ProductID string            `json:"productId"`
		SKUID     string            `json:"skuId"`
		Quantity  int32             `json:"quantity"`
		Answers   map[string]string `json:"controlAnswers"`
	}
	items := append([]OrderItemInput(nil), in.Items...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].ProductID == items[j].ProductID {
			return items[i].SkuID < items[j].SkuID
		}
		return items[i].ProductID < items[j].ProductID
	})
	rows := make([]item, 0, len(items))
	emptyMap := func(v map[string]string) map[string]string {
		if v == nil {
			return map[string]string{}
		}
		return v
	}
	for _, it := range items {
		answers := it.ControlAnswers
		if answers == nil {
			answers = in.ControlAnswers // Match prepareServices fallback semantics.
		}
		rows = append(rows, item{fmt.Sprint(it.ProductID), fmt.Sprint(it.SkuID), it.Quantity, emptyMap(answers)})
	}
	raw, err := json.Marshal(struct {
		Version      int               `json:"version"`
		SubsiteID    string            `json:"subsiteId"`
		UserID       string            `json:"userId"`
		Items        []item            `json:"items"`
		Answers      map[string]string `json:"controlAnswers"`
		GuestContact string            `json:"guestContact"`
		Contact      string            `json:"contact"`
		CouponCode   string            `json:"couponCode"`
		UsePoints    bool              `json:"usePoints"`
		RefCode      string            `json:"refCode"`
	}{1, fmt.Sprint(in.SubsiteID), fmt.Sprint(in.UserID), rows, emptyMap(in.ControlAnswers), in.GuestContact, in.Contact, in.CouponCode, in.UsePoints, in.RefCode})
	if err != nil {
		return "", "", "", err
	}
	if len(raw) > 64<<10 {
		return "", "", "", fmt.Errorf("order.FORM_INVALID: 下单参数过大")
	}
	mac := hmac.New(sha256.New, uc.Fingerprinter.key)
	mac.Write(raw)
	fingerprint = "v1:v1:" + hex.EncodeToString(mac.Sum(nil))
	return
}
func (uc *OrderUsecase) replay(ctx context.Context, in CreateOrderInput, key, legacy, fp string) (*CreateOrderResult, error) {
	if key == "" {
		return nil, nil
	}
	c := data.Client(ctx, uc.Data)
	previous, err := c.Order.Query().Where(order.IdempotencyKey(key)).Only(ctx)
	if ent.IsNotFound(err) {
		old, e := c.Order.Query().Where(order.IdempotencyKey(legacy)).Exist(ctx)
		if e != nil {
			return nil, e
		}
		if old {
			return nil, fmt.Errorf("order.IDEMPOTENCY_CONFLICT")
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if previous.UserID != in.UserID || previous.SubsiteID != in.SubsiteID || previous.RequestFingerprint == "" || !hmac.Equal([]byte(previous.RequestFingerprint), []byte(fp)) {
		return nil, fmt.Errorf("order.IDEMPOTENCY_CONFLICT")
	}
	if in.UserID == 0 && (in.QueryPassword == "" || !crypto.VerifyPassword(previous.QueryPasswordHash, in.QueryPassword)) {
		return nil, fmt.Errorf("order.IDEMPOTENCY_CONFLICT")
	}
	return &CreateOrderResult{OrderNo: previous.OrderNo, TotalCents: previous.TotalAmount, ExpiresAt: previous.ExpiredAt}, nil
}

// ReplayOrder is used before one-shot captcha checks; it never creates an order.
func (uc *OrderUsecase) ReplayOrder(ctx context.Context, in CreateOrderInput) (*CreateOrderResult, error) {
	if err := validatePurchaseItems(in); err != nil {
		return nil, err
	}
	key, legacy, fp, err := uc.idempotency(in)
	if err != nil {
		return nil, err
	}
	return uc.replay(ctx, in, key, legacy, fp)
}
