package data

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
)

const DefaultBaseCurrency = "CNY"

var currencyCodePattern = regexp.MustCompile(`^[A-Z]{3}$`)

// BaseCurrency is the accounting currency. A missing setting is the legacy CNY
// default; database failures and invalid values must never silently change money.
func BaseCurrency(ctx context.Context, d *Data) (string, error) {
	row, err := Client(ctx, d).Setting.Query().Where(setting.Group("i18n"), setting.Key("base_currency")).Only(ctx)
	if ent.IsNotFound(err) {
		return DefaultBaseCurrency, nil
	}
	if err != nil {
		return "", err
	}
	return parseBaseCurrency(row.Value)
}

func parseBaseCurrency(value json.RawMessage) (string, error) {
	var code string
	if json.Unmarshal(value, &code) != nil || !currencyCodePattern.MatchString(code) {
		return "", fmt.Errorf("settings.INVALID_BASE_CURRENCY: 基础货币配置无效")
	}
	return code, nil
}

// LockCurrencyConfiguration serializes a currency change with first-time pricing
// and account creation across processes and all supported database dialects.
// The caller must keep the surrounding transaction open until its write commits.
func LockCurrencyConfiguration(ctx context.Context, d *Data) (string, error) {
	if _, ok := ctx.Value(txKey{}).(*ent.Tx); !ok {
		return "", fmt.Errorf("data: currency lock requires a transaction")
	}
	c := Client(ctx, d)
	if err := c.Setting.Create().SetGroup("i18n").SetKey("base_currency").SetValue(json.RawMessage(`"CNY"`)).
		OnConflictColumns(setting.FieldGroup, setting.FieldKey).Ignore().Exec(ctx); err != nil {
		return "", err
	}
	if _, err := c.Setting.Update().Where(setting.Group("i18n"), setting.Key("base_currency")).SetKey("base_currency").Save(ctx); err != nil {
		return "", err
	}
	q := c.Setting.Query().Where(setting.Group("i18n"), setting.Key("base_currency"))
	if d.Dialect != db.SQLite {
		// A locking read sees the latest committed value after waiting for a
		// concurrent switch, even with MySQL REPEATABLE READ snapshots.
		q = q.ForUpdate()
	}
	row, err := q.Only(ctx)
	if err != nil {
		return "", err
	}
	return parseBaseCurrency(row.Value)
}

// CurrencyTx protects creation of an entity whose amounts have no separate
// currency column (products, coupons, gift cards, and supplier accounts).
func CurrencyTx(ctx context.Context, d *Data, fn func(context.Context) error) error {
	return Tx(ctx, d, func(ctx context.Context) error {
		if _, err := LockCurrencyConfiguration(ctx, d); err != nil {
			return err
		}
		return fn(ctx)
	})
}
