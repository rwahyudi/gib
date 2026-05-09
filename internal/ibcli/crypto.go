package ibcli

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"time"
)

const encryptedPasswordPrefix = "enc:v1:"

func generateFernetKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(key), nil
}

func encryptFernet(keyText, plaintext string) (string, error) {
	key, err := decodeFernetKey(keyText)
	if err != nil {
		return "", err
	}
	signingKey := key[:16]
	encryptionKey := key[16:]

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", err
	}
	iv := make([]byte, block.BlockSize())
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}

	padded := pkcs7Pad([]byte(plaintext), block.BlockSize())
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	token := make([]byte, 0, 1+8+len(iv)+len(ciphertext)+32)
	token = append(token, 0x80)
	timestamp := make([]byte, 8)
	binary.BigEndian.PutUint64(timestamp, uint64(time.Now().Unix()))
	token = append(token, timestamp...)
	token = append(token, iv...)
	token = append(token, ciphertext...)

	mac := hmac.New(sha256.New, signingKey)
	mac.Write(token)
	token = append(token, mac.Sum(nil)...)
	return encryptedPasswordPrefix + base64.URLEncoding.EncodeToString(token), nil
}

func decryptFernet(keyText, encrypted string) (string, error) {
	if !bytes.HasPrefix([]byte(encrypted), []byte(encryptedPasswordPrefix)) {
		return encrypted, nil
	}
	key, err := decodeFernetKey(keyText)
	if err != nil {
		return "", err
	}
	tokenText := encrypted[len(encryptedPasswordPrefix):]
	token, err := base64.URLEncoding.DecodeString(tokenText)
	if err != nil {
		return "", err
	}
	if len(token) < 1+8+aes.BlockSize+sha256.Size || token[0] != 0x80 {
		return "", cliError("invalid encrypted password token")
	}

	body := token[:len(token)-sha256.Size]
	expectedMAC := token[len(token)-sha256.Size:]
	mac := hmac.New(sha256.New, key[:16])
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), expectedMAC) {
		return "", cliError("encrypted password authentication failed")
	}

	ivStart := 1 + 8
	ivEnd := ivStart + aes.BlockSize
	ciphertext := token[ivEnd : len(token)-sha256.Size]
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return "", cliError("invalid encrypted password ciphertext")
	}
	block, err := aes.NewCipher(key[16:])
	if err != nil {
		return "", err
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, token[ivStart:ivEnd]).CryptBlocks(plaintext, ciphertext)
	plaintext, err = pkcs7Unpad(plaintext, aes.BlockSize)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func decodeFernetKey(keyText string) ([]byte, error) {
	key, err := base64.URLEncoding.DecodeString(string(bytes.TrimSpace([]byte(keyText))))
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, cliError("invalid encryption key length")
	}
	return key, nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padText := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(append([]byte{}, data...), padText...)
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, cliError("invalid padding size")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, cliError("invalid padding")
	}
	for _, value := range data[len(data)-padding:] {
		if int(value) != padding {
			return nil, cliError("invalid padding")
		}
	}
	return data[:len(data)-padding], nil
}
