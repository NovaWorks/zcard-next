package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supplier"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
)

func runSupplyWalletMigration(args []string) error {
	fs := flag.NewFlagSet("supply-wallet-migrate", flag.ContinueOnError)
	dir := fs.String("conf", "configs", "existing account/supply database configuration")
	mapping := fs.String("owners", "", "JSON object of supplier account ID to owner user ID; required for ownerless accounts")
	apply := fs.Bool("apply", false, "atomically transfer reconciled legacy balances; default is read-only preflight")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	owners := map[uint64]uint64{}
	if *mapping != "" {
		f, err := os.Open(*mapping)
		if err != nil {
			return err
		}
		defer f.Close()
		dec := json.NewDecoder(io.LimitReader(f, 4<<20))
		opening, err := dec.Token()
		if err != nil || opening != json.Delim('{') {
			return fmt.Errorf("mapping must be a JSON object")
		}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			raw, ok := key.(string)
			if !ok {
				return fmt.Errorf("invalid account ID")
			}
			id, err := strconv.ParseUint(raw, 10, 64)
			if err != nil || id == 0 || strconv.FormatUint(id, 10) != raw {
				return fmt.Errorf("invalid account ID")
			}
			if _, exists := owners[id]; exists {
				return fmt.Errorf("duplicate supplier account mapping: %d", id)
			}
			var owner uint64
			if err = dec.Decode(&owner); err != nil || owner == 0 {
				return fmt.Errorf("owner must be a positive user ID")
			}
			owners[id] = owner
		}
		closing, err := dec.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("incomplete mapping")
		}
		var extra any
		if err = dec.Decode(&extra); err != io.EOF {
			return fmt.Errorf("mapping must contain exactly one JSON object")
		}

	}
	bc, err := loadBootstrap(*dir)
	if err != nil {
		return err
	}
	if bc.Data == nil {
		return fmt.Errorf("existing database configuration required")
	}
	d, closeDB, err := data.NewData(bc.Data)
	if err != nil {
		return err
	}
	defer closeDB()
	repo := supplier.NewSupplierRepoImpl(d, nil, wallet.ProvidePortWallet(wallet.NewWalletRepoImpl(d)))
	report, err := repo.MigrateSharedWallet(context.Background(), owners, *apply)
	if e := json.NewEncoder(os.Stdout).Encode(report); e != nil {
		return e
	}
	if err != nil {
		return err
	}
	if !report.Ready {
		return fmt.Errorf("migration preflight failed; no balances transferred")
	}
	return nil
}
