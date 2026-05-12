package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	secureLogHeaderPrefix     = "XBOARD_SECURE_LOG_V1 "
	secureLogUploadLimitBytes = 20 * 1024 * 1024
	secureLogPrivateKeyPEM    = `-----BEGIN PRIVATE KEY-----
MIIEwAIBADANBgkqhkiG9w0BAQEFAASCBKowggSmAgEAAoIBAQDag6f5NCiWQzy3
eYJ2F52n6pJa8zgrjnVNlDQuu4naKdknIcEXGS9TcvGkpRV3B0NdmNb3O/bValLL
BrACeeQRbS9neTHFsIj5yXZlh2UL5meN1T7HSiyLIK3C3klP+wJFsbpLwI3KHfKg
uIt9tuYqQCxg9mg7ism8BnhA7AjbXxiuQ4vNogA+slAaqLJsQsEM2LAURPXv4asX
HQ6U7tgn9tw86IJqXvgO7PACvmgw6f8zpc3xiWLpVBJmxLa3Nv/+Gt1AlANze5AU
+qhO8qhlRLTPvvyyhxVL59L+yvnQxAtYGYCDNXIkHHzSIMVoiS83soqhAtjC9AQz
2pmuFPZLAgMBAAECggEBAIEZx+Q0LMaacwTzhWDAEyViMZYKnOUfBa8QIMR7iLac
gu/bwXkkKBHll17vKf9pCyQBaQApLWxppQDOsq7D1Tt2hstbj0x9QHBT1t+lXs3p
EsV5d93GtQp+BCtdqXLXmkATAT5ARYVkrDTI06EyrknIIHApJOwI06eDKwkwawsv
zDwireaGdS/h1tuLnJ3gZbndkznuSA/4ewtAX+LwkCCg8dMy01GyEypTW3/3sxRy
0ht1kdQVqtMzWnhJQvN2SKXGfXQX/WrWNdYxb9rEwMpl4PdfGeUoOZlxLWxQmh6C
ou/RIt6GHhJM0yz24VtkreYjT8TLmpdgCFd8v0efsgECgYEA+hCgarIwbLydb/y6
r0hRsOPP80m3AJ1v83KuhG4n+4nmNSVm4m8HW4HmgWiZWiY6vqS2M4hxggEZXUZQ
71cS+tpQKf+m8Jwcld5ymKdE+uj/76UC8FB0EdqHMvbtPqfRl8/GrgBwvsZRWQO3
42fxocMZCutsM9wH2AW27UE+yH0CgYEA37NUkgZdFQ2Qz6G4v5Y1louaomau8zfI
Y2mlQ0DnGyDqBmB3o1ZIIn+nL9l9J1mPr1OlQRBlUgnhi8sqLxEO44a04xqzn04k
yocXdz5kSssj5tMk34CGwCcLh0solZyyjCoKw88V91Sfu1Tm4k38nyfluxtiY+4m
x4djas85PGcCgYEAqFlzNgGaekoND+ysXf8pCBaG1DpHWsGjMdl+RifHASAYfKUe
e8jVwjRU08BwpXFhUSGgjFcKW8STp+kD6e3MGFfLakrzv77Ju9fTfJP365fbXiHQ
NatkSPS+2n/Evs7KWxMFpfUj8jufXncTYKSE1yt6e5B8+vjhyvwl59pqAx0CgYEA
hHR1zeTwtqeKqDaU4vQ5FMPisuhkDOVpxNtoHHNQpDKP/2idTlynZ634O4/m2Cbi
uiin/+eKZtIs9447kxThoP1BG/vSgbBOfpEQ5u1Niy/POTyqZ6B9qUc1P03UYQog
enfmWdzDn+g+kDiMYVFWFJMWJvzm/E6mLZzP1A2RUV8CgYEA6JrXF//G1Opv9Enn
8ob8JS4yqutHmdTBny3/kuOwLA76uF5bvK57v3oeM3QQ1xAnAgk5QRZtngMkP6kN
651yFQS8qC+kOPEvo04Q02Epcl48n7Hto4aJxHe5cFN43G9R7jphXZrFY8ZoAPkh
Fre75JxcK3drxSKBOi6b8eB6xaw=
-----END PRIVATE KEY-----`
)

type secureLogHeader struct {
	Algorithm string `json:"alg"`
	Key       string `json:"key"`
	CreatedAt string `json:"createdAt"`
}

type secureLogRecord struct {
	Nonce      string `json:"n"`
	Ciphertext string `json:"c"`
}

type secureLogEvent struct {
	Time    string `json:"time"`
	Source  string `json:"source"`
	Message string `json:"message"`
	Error   string `json:"error"`
	Stack   string `json:"stack"`
}

func (h *Handlers) DecryptSecureLog(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, secureLogUploadLimitBytes)
	if err := r.ParseMultipartForm(secureLogUploadLimitBytes); err != nil {
		jsonError(w, "日志文件过大或表单无效，最大支持 20MB", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		jsonError(w, "请选择 .xblog 日志文件", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if header != nil && !strings.HasSuffix(strings.ToLower(header.Filename), ".xblog") {
		jsonError(w, "只支持 .xblog 日志文件", http.StatusBadRequest)
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, secureLogUploadLimitBytes+1))
	if err != nil {
		jsonError(w, "读取日志文件失败", http.StatusBadRequest)
		return
	}
	if int64(len(data)) > secureLogUploadLimitBytes {
		jsonError(w, "日志文件过大，最大支持 20MB", http.StatusBadRequest)
		return
	}

	output, err := decryptSecureLogBytes(data)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	jsonResponse(w, map[string]string{
		"filename": header.Filename,
		"content":  output,
	})
}

func decryptSecureLogBytes(data []byte) (string, error) {
	privateKey, err := parseSecureLogPrivateKey()
	if err != nil {
		return "", err
	}

	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) == 0 {
		return "", errors.New("无效日志文件：内容为空")
	}

	headerLine := strings.TrimRight(string(lines[0]), "\r")
	if !strings.HasPrefix(headerLine, secureLogHeaderPrefix) {
		return "", fmt.Errorf("无效日志文件：缺少 %s 头", secureLogHeaderPrefix)
	}

	var header secureLogHeader
	if err := json.Unmarshal([]byte(strings.TrimPrefix(headerLine, secureLogHeaderPrefix)), &header); err != nil {
		return "", fmt.Errorf("日志头解析失败：%w", err)
	}
	if header.Key == "" {
		return "", errors.New("日志头缺少加密会话密钥")
	}

	encryptedKey, err := base64.StdEncoding.DecodeString(header.Key)
	if err != nil {
		return "", fmt.Errorf("日志会话密钥不是有效 base64：%w", err)
	}
	sessionKey, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, encryptedKey, nil)
	if err != nil {
		return "", fmt.Errorf("日志会话密钥解密失败：%w", err)
	}

	block, err := aes.NewCipher(sessionKey)
	if err != nil {
		return "", fmt.Errorf("AES 初始化失败：%w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("AES-GCM 初始化失败：%w", err)
	}

	var output strings.Builder
	for index, rawLine := range lines[1:] {
		line := strings.TrimSpace(string(rawLine))
		if line == "" {
			continue
		}
		plain, err := decryptSecureLogLine(aead, line)
		if err != nil {
			output.WriteString(fmt.Sprintf("[decrypt-error line=%d] %v\n", index+2, err))
			continue
		}
		output.WriteString(formatSecureLogEvent(plain))
		output.WriteString("\n")
	}

	return output.String(), nil
}

func decryptSecureLogLine(aead cipher.AEAD, line string) ([]byte, error) {
	var record secureLogRecord
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return nil, fmt.Errorf("记录解析失败：%w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(record.Nonce)
	if err != nil {
		return nil, fmt.Errorf("nonce 不是有效 base64：%w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(record.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("密文不是有效 base64：%w", err)
	}
	return aead.Open(nil, nonce, ciphertext, nil)
}

func formatSecureLogEvent(plain []byte) string {
	var event secureLogEvent
	if err := json.Unmarshal(plain, &event); err != nil {
		return fmt.Sprintf("[format-error] %v raw=%s", err, string(plain))
	}

	var output strings.Builder
	output.WriteString("[")
	output.WriteString(event.Time)
	output.WriteString("][")
	output.WriteString(event.Source)
	output.WriteString("] ")
	output.WriteString(event.Message)
	if event.Error != "" {
		output.WriteString("\n  error: ")
		output.WriteString(event.Error)
	}
	if event.Stack != "" {
		output.WriteString("\n")
		output.WriteString(event.Stack)
	}
	return output.String()
}

func parseSecureLogPrivateKey() (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(secureLogPrivateKeyPEM))
	if block == nil {
		return nil, errors.New("内置日志私钥解析失败")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("内置日志私钥格式无效：%w", err)
	}
	privateKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("内置日志私钥不是 RSA 私钥")
	}
	return privateKey, nil
}
