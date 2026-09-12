package inventory

// SealLottery 使用独立用途 AAD，抽奖内容不能被当作商品卡密解密。
func (c *CardCipher) SealLottery(plain, reference string, subsiteID uint64) ([]byte, error) {
	return c.box.Seal([]byte(plain), []byte("lottery:"+reference+":subsite:"+u64(subsiteID)))
}
func (c *CardCipher) OpenLottery(ciphertext []byte, reference string, subsiteID uint64) (string, error) {
	p, err := c.box.Open(ciphertext, []byte("lottery:"+reference+":subsite:"+u64(subsiteID)))
	return string(p), err
}
