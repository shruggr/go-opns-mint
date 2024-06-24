package opns

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"

	ec "github.com/bitcoin-sv/go-sdk/primitives/ec"
	"github.com/bitcoin-sv/go-sdk/script"
	"github.com/bitcoin-sv/go-sdk/transaction"
	feemodel "github.com/bitcoin-sv/go-sdk/transaction/fee_model"
	"github.com/bitcoin-sv/go-sdk/transaction/template/p2pkh"
	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"github.com/shruggr/1sat-indexer/lib"
)

const DIFFICULTY = 22

var comp = big.NewInt(0)

type OpnsMine struct {
	Outpoint *lib.Outpoint
	Script   []byte
}

type Txo struct {
	Outpoint *lib.Outpoint `json:"outpoint"`
	Satoshis uint64        `json:"satoshis"`
}

var genesis, _ = hex.DecodeString("25cb9c17772641ba2374a8d74f729aad921932fef5e2c76642f279a38e55b75800000000")

var priv *ec.PrivateKey
var defaultOwner *script.Script
var fundingAddress *script.Address
var fundingScript *script.Script
var fees = &feemodel.SatoshisPerKilobyte{Satoshis: 1}
var rdb *redis.Client

func init() {
	godotenv.Load(".env")
	var err error

	if wif := os.Getenv("WIF"); wif == "" {
		log.Fatal("WIF env is required")
	} else if priv, err = ec.PrivateKeyFromWif(wif); err != nil {
		log.Fatal(err)
	} else if fundingAddress, err = script.NewAddressFromPublicKey(priv.PubKey(), true); err != nil {
		log.Fatal(err)
	} else if fundingScript, err = p2pkh.Lock(fundingAddress); err != nil {
		log.Fatal(err)
	}
	ownerAdd, _ := script.NewAddressFromString(os.Getenv("OWNER_ADDRESS"))
	if defaultOwner, err = p2pkh.Lock(ownerAdd); err != nil {
		log.Fatal("OWNER_ADDRESS env is required")
	}
	rdb = redis.NewClient(&redis.Options{
		Addr:     os.Getenv("REDIS"),
		Password: "", // no password set
		DB:       0,  // use default DB
	})
}

func MineDomain(ctx context.Context, domain string, ownerScript *script.Script) (string, error) {
	mine, err := LookupMine(domain)
	if err != nil {
		log.Println("LookupMine", err)
		return "", err
	}
	toMine, _ := strings.CutPrefix(domain, mine.Domain)
	var lastTxid string
	for _, char := range []byte(toMine) {
		var tx *transaction.Transaction
		owner := defaultOwner
		if mine.Domain+string(char) == domain {
			owner = ownerScript
		}
		if tx, err = mine.BuildUnlockTx(char, owner); err != nil {
			log.Println("BuildUnlockTx", err)
			return "", err
		} else if err = FundAndSignTx(ctx, tx); err != nil {
			log.Println("FundAndSignTx", err)
			return "", err
		} else if resp, err := http.Post(
			"https://ordinals.gorillapool.io/api/tx/bin",
			"application/octet-stream",
			bytes.NewReader(tx.Bytes()),
		); err != nil {
			log.Println("Post", err)
			return "", err
		} else if resp.StatusCode != 200 {
			defer resp.Body.Close()
			res, _ := io.ReadAll(resp.Body)
			log.Printf("tx failed - %d %s", resp.StatusCode, string(res))
			log.Printf("rawtx: %x\n", tx.Bytes())
			if _, err := rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
				for _, input := range tx.Inputs[1:] {
					outpoint := lib.NewOutpoint(input.SourceTXID, input.SourceTxOutIndex)
					if err := pipe.LPush(ctx, "utxos", fmt.Sprintf("%s:%d", outpoint.String(), *input.PreviousTxSatoshis())).Err(); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return "", err
			}
			return "", fmt.Errorf("tx failed - %d %s", resp.StatusCode, string(res))
		} else {
			outpoint := lib.NewOutpoint(tx.TxIDBytes(), 1)
			lastTxid = tx.TxID()
			log.Println("Mined", string(char), lastTxid)
			if _, err := rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
				for vout, output := range tx.Outputs {
					if output.Change {
						if err := pipe.LPush(ctx, "utxos", fmt.Sprintf("%s_%d:%d", lastTxid, vout, output.Satoshis)).Err(); err != nil {
							return err
						}
					}
				}
				return nil
			}); err != nil {
				return "", err
			} else if mine, err = (&Opns{}).FromScript(outpoint, tx.Outputs[1].LockingScript); err != nil {
				log.Println("FromScript", err)
				return "", err
			}
		}
	}
	return lastTxid, nil
}

func LookupOpns(domain string) (*OpnsMine, error) {
	if resp, err := http.Post(
		fmt.Sprintf("https://ordinals.gorillapool.io/api/opns/%s", domain),
		"application/json",
		bytes.NewReader([]byte(`{"opns":{"domain":"%s","status":1}}`)),
	); err != nil {
		log.Println("Opns Post", err)
		return nil, err
	} else if resp.StatusCode == 404 {
		return nil, nil
	} else {
		defer resp.Body.Close()
		result := OpnsMine{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, err
		}
		return &result, nil
	}
}

func LookupMine(domain string) (*Opns, error) {
	url := fmt.Sprintf("https://ordinals.gorillapool.io/api/opns/%s/mine", domain)
	if opns, err := LookupOpns(domain); err != nil {
		log.Println("LookupOpns", err)
		return nil, err
	} else if opns != nil {
		return nil, errors.New("not-available")
	} else if resp, err := http.Get(url); err != nil {
		log.Println("Lookup Post", err)
		return nil, err
	} else {
		defer resp.Body.Close()
		result := OpnsMine{}
		if body, err := io.ReadAll(resp.Body); err != nil {
			log.Println("ReadAll", err)
			return nil, err
		} else if err := json.Unmarshal(body, &result); err != nil {
			log.Println("Decode", err)
			return nil, err
		}

		return (&Opns{}).FromScript(result.Outpoint, (*script.Script)(&result.Script))
	}
}

func FundAndSignTx(ctx context.Context, tx *transaction.Transaction) error {
	satsIn := tx.TotalInputSatoshis()
	satsOut := tx.TotalOutputSatoshis()
	changeScript, err := p2pkh.Lock(fundingAddress)
	if err != nil {
		return err
	}
	changeOutput := &transaction.TransactionOutput{
		LockingScript: changeScript,
		Satoshis:      0,
		Change:        true,
	}
	tx.AddOutput(changeOutput)
	fee, err := fees.ComputeFee(tx)
	if err != nil {
		return err
	}
	unlock, err := p2pkh.Unlock(priv, nil)
	if err != nil {
		return err
	}

	for satsIn < satsOut+fee {
		if utxo, err := rdb.LPop(ctx, "utxos").Result(); err != nil {
			return err
		} else {
			var outpoint *lib.Outpoint
			parts := strings.Split(utxo, ":")
			if outpoint, err = lib.NewOutpointFromString(parts[0]); err != nil {
				return err
			} else if sats, err := strconv.Atoi(parts[1]); err != nil {
				return err
			} else if err = tx.AddInputsFromUTXOs(&transaction.UTXO{
				TxID:                    outpoint.Txid(),
				Vout:                    outpoint.Vout(),
				LockingScript:           fundingScript,
				Satoshis:                uint64(sats),
				UnlockingScriptTemplate: unlock,
			}); err != nil {
				return err
			} else if fee, err = fees.ComputeFee(tx); err != nil {
				return err
			} else {
				satsIn += uint64(sats)
			}
		}
	}
	tx.Fee(fees, transaction.ChangeDistributionEqual)

	return tx.Sign()
}

func RefreshBalance(ctx context.Context) (uint64, error) {
	txos := []*Txo{}
	url := fmt.Sprintf("https://ordinals.gorillapool.io/api/txos/address/%s/unspent", fundingAddress.AddressString)
	if req, err := http.Get(url); err != nil {
		return 0, err
	} else if body, err := io.ReadAll(req.Body); err != nil {
		return 0, err
	} else if err := json.Unmarshal(body, &txos); err != nil {
		return 0, err
	} else {
		balance := uint64(0)
		if _, err := rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Del(ctx, "utxos")
			for _, txo := range txos {
				pipe.RPush(ctx, "utxos", fmt.Sprintf("%s:%d", txo.Outpoint.String(), txo.Satoshis))
				balance += txo.Satoshis
			}
			return nil
		}); err != nil {
			return 0, fiber.ErrInternalServerError
		}
		return balance, nil
	}
}
