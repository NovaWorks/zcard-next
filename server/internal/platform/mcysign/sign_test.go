package mcysign

import "testing"

func TestPHPSignatureWithEscapedSecret(t *testing.T) {
	// PHP: md5(urldecode(http_build_query($params)."&key=a+b%2Bc%ZZ"))
	params := map[string]string{"sku_id": "1", "quantity": "1"}
	const want = "262cc9e6889fa8c03c2457d4f99aa564"
	if got := Sign(params, "a+b%2Bc%ZZ"); got != want {
		t.Fatal("signature differs from PHP", got)
	}
	if !Verify(params, "a+b%2Bc%ZZ", want) || Verify(params, "wrong", want) {
		t.Fatal("signature verification failed")
	}
}
