package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"runtime"

	"github.com/gin-gonic/gin"
	"github.com/libsv/go-bt/v2"
)

const DIFFICULTY = 22

var comp = big.NewInt(0)
var CONCURRENCY int
var limit chan struct{}

type Pow struct {
	Nonce  string `json:"nonce"`
	Hashes uint64 `json:"hashes"`
}

func init() {
	CONCURRENCY = runtime.NumCPU()
	limit = make(chan struct{}, CONCURRENCY)
	fmt.Printf("Concurrency: %d\n", CONCURRENCY)
}

func main() {
	r := gin.Default()
	r.GET("/mine/:char/:pow", func(c *gin.Context) {
		char := c.Param("char")[0]
		pow, err := hex.DecodeString(c.Param("pow"))
		if err != nil {
			c.String(400, "Invalid POW")
			return
		}
		c.JSON(200, mine(char, pow))
	})
	r.Run() // listen and serve on 0.0.0.0:8080

}

func mine(char byte, prevPow []byte) *Pow {
	var done = make(chan *Pow)
	counter := uint(0)
	for {
		select {
		case nonce := <-done:
			return nonce
		default:
			limit <- struct{}{}
			go func() {
				test := append([]byte{}, prevPow...)
				test = append(test, char)
				nonce := make([]byte, 32)
				counter++
				rand.Read(nonce)
				// nonce, _ := hex.DecodeString("3ffd296edebfae7f")
				test = append(test, nonce...)

				hash := sha256.Sum256(test)
				hash = sha256.Sum256(hash[:])

				testInt := new(big.Int).SetBytes(bt.ReverseBytes(hash[:]))
				testInt = testInt.Rsh(testInt, uint(256-DIFFICULTY))
				<-limit
				if testInt.Cmp(comp) == 0 {
					fmt.Printf("Test: %b %x\n", testInt, bt.ReverseBytes(hash[:]))
					fmt.Printf("Found: %x\n", nonce)
					done <- &Pow{
						Nonce:  hex.EncodeToString(nonce),
						Hashes: uint64(counter),
					}

				}
			}()
		}
	}
}
