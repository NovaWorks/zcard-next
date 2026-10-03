package inventory

func (c *CardCipher) SealOrderAccess(token, no string, subsiteID uint64) ([]byte, error) {
	return c.box.Seal([]byte(token), []byte("order-access:v1:"+no+":subsite:"+u64(subsiteID)))
}

func (c *CardCipher) OpenOrderAccess(ciphertext []byte, no string, subsiteID uint64) (string, error) {
	plain, err := c.box.Open(ciphertext, []byte("order-access:v1:"+no+":subsite:"+u64(subsiteID)))
	return string(plain), err
}
