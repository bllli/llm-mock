package main

import (
	"crypto/sha1"
	"fmt"
)

func main() {
	url := "https://openaipublic.blob.core.windows.net/encodings/cl100k_base.tiktoken"
	cacheKey := fmt.Sprintf("%x", sha1.Sum([]byte(url)))
	fmt.Println(cacheKey)
}
