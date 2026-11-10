package crt

import (
	"crypto/tls"
	"fmt"
)

var crtBytes = []byte(`-----BEGIN CERTIFICATE-----
MIIDzTCCArWgAwIBAgIUcaHkCnvXdKNSvKb/lVOZxRck6T0wDQYJKoZIhvcNAQEL
BQAwajELMAkGA1UEBhMCQ04xEDAOBgNVBAgMB0JlaWppbmcxEDAOBgNVBAcMB0Jl
aWppbmcxFzAVBgNVBAoMDkxpY2VuY2VNYW5hZ2VyMQswCQYDVQQLDAJJVDERMA8G
A1UEAwwIY2VzdGMuY24wIBcNMjUxMjIzMDY0NzEwWhgPMjEyNTExMjkwNjQ3MTBa
MGoxCzAJBgNVBAYTAkNOMRAwDgYDVQQIDAdCZWlqaW5nMRAwDgYDVQQHDAdCZWlq
aW5nMRcwFQYDVQQKDA5MaWNlbmNlTWFuYWdlcjELMAkGA1UECwwCSVQxETAPBgNV
BAMMCGNlc3RjLmNuMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAlKXq
4yIs1ktzabIZNVU9kAvrUtGK4tZ5ldVFynTX88VhUR2d89iWS7CY47OGr42pP7uw
RuXCg1H+3NB6bsMdqR9GIeemNchtzMbXQYizpNKFGcofgiatNovupDU6pzCEtQ/0
IWHCCS/U9D/6iOBTjCblPd5bKJ9Wxo/IIJwNVC6WoBwGx4mEMF4WvFu1qIBFfjPk
sIftT5P8c7cS/IEVe6vk/iEm/P5pLJtamkJeRXM43AHOHxOsE7xeG83XQrtTaGfA
5o20M3N7+vlU1ur81YVmYVIm99FxQ80UpGJi1+QouF6Gly6bq8rdyX2hqgwE0/0g
D4vJTaognh/HS6r50QIDAQABo2kwZzALBgNVHQ8EBAMCBDAwEwYDVR0lBAwwCgYI
KwYBBQUHAwEwJAYDVR0RBB0wG4IIY2VzdGMuY26CCWxvY2FsaG9zdIcEfwAAATAd
BgNVHQ4EFgQUVh3hUTfTQiUripqP/WHj4pUojB8wDQYJKoZIhvcNAQELBQADggEB
ABhXL/OsJrcbJH2krWE6BoQWpRoRaSsJOoSN4wWgGzC3QUGylGdp+5z3pL4Ki124
Cf4F7gKtrVe1ucZCYJWMf6hlj+o3ja4IHbDAHHQEsl14c/+UZq3sRjsNK52EpxKR
aRFw1nfmFDMK9RrejpFf1dIBAucjzccKko1OUuvUJS0GLXUTjF23pc+xxuHxCSQK
hStU/CWgo1P5B15S4y0DofCdMjiTaGIpJl5YvVKC/Q7KOJYHGXYPpsNWmWipbiNt
I37DFcPC/+ln+jUHcCKVkJbxaLvdnthF5qiQpEYGi9H8oothVbHR+4wr6MW5nKgB
9VGEJgV6wpdQZziQm4pLnYA=
-----END CERTIFICATE-----`)

var keyBytes = []byte(`-----BEGIN PRIVATE KEY-----
MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCUperjIizWS3Np
shk1VT2QC+tS0Yri1nmV1UXKdNfzxWFRHZ3z2JZLsJjjs4avjak/u7BG5cKDUf7c
0Hpuwx2pH0Yh56Y1yG3MxtdBiLOk0oUZyh+CJq02i+6kNTqnMIS1D/QhYcIJL9T0
P/qI4FOMJuU93lson1bGj8ggnA1ULpagHAbHiYQwXha8W7WogEV+M+Swh+1Pk/xz
txL8gRV7q+T+ISb8/mksm1qaQl5FczjcAc4fE6wTvF4bzddCu1NoZ8DmjbQzc3v6
+VTW6vzVhWZhUib30XFDzRSkYmLX5Ci4XoaXLpuryt3JfaGqDATT/SAPi8lNqiCe
H8dLqvnRAgMBAAECggEAAxiMSoIchNNOFl1FqAIkKckMw/UeJ1SsFDg8l4+MzJx/
lw/WyYSJF08YlRP47fS1aeDV5lkcTq29URIkvaQxzMMca+H6KMg5jLR2uNxA8RGr
IN8QPk7ByGc8T3ZWGBOzLN314cDaaKRy0wo21KVW6h4og7wLAh7k8NZfZrn3T1mv
qRqlpJAkKDObfcsbG4mnhi9aHSdSzx+oW4fsO4LDvAtgkeEde5POBc8yVZy1umyz
UzA3LQ6CXMNeIYqMoyq5hwVHJoH+HmsQZ+MxmamCGWeKNeNWzga92LabpoEGaLBR
B8b7Mqd9/qFgvWlhHoP7Hy01Fp8jMv3Mdd3tlmkoIQKBgQC4eGIYdjp+8beXEXXL
d+wCaBrDljpJx886y9U2GNyv9oomJZaoxQBVtukoO+Ha7gcA0cALfgpKOk9xd8Sb
/b+48FTT4dsi6vAcjkMRo9cEuPd2bqgGo+bW82ABmhzbDKVtUHFKaIYZ87xFNznx
t6jtLWq4pPSHUrRcfmjOCmCNMQKBgQDOSZ2uXnpc5bM0WRIsOpZuRwO0KvLhleln
Rx85zLnWVavbnFwlZiiz0h6HgeAFd6AH6AVGpsTqp4EtOTOk2HAqywJslXNxhW8d
XiJm0KN1Nwg5gZVah7I4O0+wZpL1uCR98NzXKKfwh2YaSv6sWCnWzzKeluS0VEJ/
IaV4JHGOoQKBgBvwb7sQvQ9ZANtqk5K7mQ1MQzF4YV+nWaWoFaTZ4yLrjkT00HrD
S6evymSDnVORVZI0MogLEPfpGPswjqCpsA4MqGxm+s+xEajhixry27Ala6WOYPiQ
fnfz6IiVlOVAZxXFZ0NDzXvExLEfCjGoBXfWtyE8KqUMS7DunGUIgVMhAoGABHdt
lZfwGFDU535COXjvRuX8u4ruhVnkmtrcXzkCkRR7yx6AlEK5AOO8y3xRaGzlpyYr
7xKvUtFbp6oEZz5Ud2i73bQCsuOLu1wNbKJ515DgebZHkJU9k6AdIpml9vIMwDlV
3sa/lCKqYEldaEfkaAVGeE6WFx137x7AYXCQkOECgYBLOPOMexjyqmpU3Y0DQouh
4pqR/N2Jra+SKWx6xe7lgF/yb312Ayi0JE5pJWufqLEu0A3nIWm2UAt/q7rI+1Yw
l52qssChk2GCBhGxoHxOLYhE4+QZLBaVok05N/H9kE5pjAzchDL8Q9UAruSCXt/X
+l0Wr9J+UQdkRe+lePfHmw==
-----END PRIVATE KEY-----`)

// LoadCertificate 从内存加载证书和密钥，返回 tls.Certificate
func LoadCertificate() (tls.Certificate, error) {
	cert, err := tls.X509KeyPair(crtBytes, keyBytes)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("解析证书和密钥失败: %w", err)
	}

	return cert, nil
}

// LoadCertificateBytes 从内存加载证书和密钥，返回原始字节数据
// 返回: (证书字节, 密钥字节, 错误)
func LoadCertificateBytes() ([]byte, []byte, error) {
	return crtBytes, keyBytes, nil
}

// LoadCertificateFromBytes 从字节数据创建 tls.Certificate
// certPEM: 证书的 PEM 格式字节数据
// keyPEM: 密钥的 PEM 格式字节数据
func LoadCertificateFromBytes(certPEM, keyPEM []byte) (tls.Certificate, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("解析证书和密钥失败: %w", err)
	}

	return cert, nil
}
