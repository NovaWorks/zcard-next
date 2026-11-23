package adapter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestACGNavigationKeepsDockingCode(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{
		{`1111`, "1111"}, {`"1111"`, "1111"}, {`"9007199254740993"`, "9007199254740993"},
		{`null`, ""}, {`0`, ""}, {`-1`, ""}, {`1.5`, ""}, {`"CA814E42E563538A"`, ""}, {`"../1111"`, ""}, {`true`, ""},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/shared/commodity/inventory" {
					_ = r.ParseForm()
					if r.Form.Get("sharedCode") != "CA814E42E563538A" {
						t.Error("inventory used web page ID instead of docking code")
					}
					fmt.Fprint(w, `{"code":200,"data":{}}`)
					return
				}
				if r.URL.Path != "/shared/commodity/items" {
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				fmt.Fprintf(w, `{"code":200,"data":[{"id":1,"name":"test","children":[{"id":%s,"code":"CA814E42E563538A","name":"地址激活","price":"1.00","stock":10}]}]}`, tt.raw)
			}))
			defer srv.Close()
			a := &acgFakaAdapter{t: newTransportWithClient(srv.URL, nil, nil, srv.Client())}
			list, err := a.ListProducts(context.Background(), 1, 100, true)
			if err != nil || len(list.Items) != 1 {
				t.Fatal(list, err)
			}
			p := list.Items[0]
			if p.ID != "CA814E42E563538A" || p.PageID != tt.want {
				t.Fatalf("API code or page ID changed: %+v", p)
			}
		})
	}
}
