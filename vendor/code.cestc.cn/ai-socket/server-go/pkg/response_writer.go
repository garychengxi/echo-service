package core

import (
	"bytes"
	"github.com/gin-gonic/gin"
)

type BufferedWriter struct {
	gin.ResponseWriter
	Buf *bytes.Buffer
}

func (w BufferedWriter) Write(b []byte) (int, error) {
	w.Buf.Write(b)
	return w.ResponseWriter.Write(b)
}
