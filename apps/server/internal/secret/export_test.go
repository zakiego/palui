package secret

// Test hooks: encrypt with a given IV, so a golden blob can be rebuilt byte for byte.
var (
	EncryptWithIV      = encryptWithIV
	EncryptBytesWithIV = encryptBytesWithIV
)
