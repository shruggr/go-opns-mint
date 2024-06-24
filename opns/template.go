package opns

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"runtime"

	"github.com/bitcoin-sv/go-sdk/script"
	"github.com/bitcoin-sv/go-sdk/transaction"
	sighash "github.com/bitcoin-sv/go-sdk/transaction/sighash"
	"github.com/bitcoin-sv/go-sdk/util"
	"github.com/shruggr/1sat-indexer/lib"
)

type Pow struct {
	Nonce  []byte `json:"nonce"`
	Hash   []byte `json:"hash"`
	Hashes uint64 `json:"hashes"`
}

type Opns struct {
	Outpoint      *lib.Outpoint  `json:"outpoint"`
	Claimed       []byte         `json:"claimed"`
	Domain        string         `json:"domain"`
	Pow           []byte         `json:"pow"`
	LockingScript *script.Script `json:"lockingScript"`
	solution      *Pow
}

type OpnsUnlocker struct {
	Opns
	Char        byte           `json:"char"`
	OwnerScript *script.Script `json:"ownerScript"`
}

func (o *Opns) FromScript(outpoint *lib.Outpoint, s *script.Script) (*Opns, error) {
	if !bytes.HasPrefix(*s, contract) {
		return nil, errors.New("invalid script")
	}
	pos := len(contract) + 2

	if opGenesis, err := s.ReadOp(&pos); err != nil {
		return nil, err
	} else if !bytes.Equal(opGenesis.Data, genesis) {
		return nil, errors.New("invalid genesis")
	} else if opClaimed, err := s.ReadOp(&pos); err != nil {
		return nil, err
	} else if opDomain, err := s.ReadOp(&pos); err != nil {
		return nil, err
	} else if opPow, err := s.ReadOp(&pos); err != nil {
		return nil, err
	} else {
		o.Outpoint = outpoint
		o.Claimed = opClaimed.Data
		o.Domain = string(opDomain.Data)
		o.Pow = opPow.Data
		o.LockingScript = s
	}
	return o, nil
}

func (o *Opns) BuildUnlockTx(char byte, ownerScript *script.Script) (*transaction.Transaction, error) {
	tx := transaction.NewTransaction()
	o.solution = o.Mine(char)
	unlock, err := o.Unlock(char, ownerScript)
	if err != nil {
		return nil, err
	}
	tx.AddInputsFromUTXOs(&transaction.UTXO{
		TxID:                    o.Outpoint.Txid(),
		Vout:                    o.Outpoint.Vout(),
		LockingScript:           o.LockingScript,
		Satoshis:                1,
		SequenceNumber:          0xffffffff,
		UnlockingScriptTemplate: unlock,
	})

	claimed := big.NewInt(0)
	claimed.SetBytes(util.ReverseBytes(o.Claimed))
	claimed.SetBit(claimed, int(char), 1)
	claimedBytes := claimed.Bytes()
	if claimedBytes[0]&0x80 != 0 {
		claimedBytes = append([]byte{0x00}, claimedBytes...)
	}
	restateScript := Lock(util.ReverseBytes(claimedBytes), o.Domain, o.solution.Hash)
	tx.AddOutput(&transaction.TransactionOutput{
		LockingScript: restateScript,
		Satoshis:      1,
	})

	newDomain := o.Domain + string(char)
	newScript := Lock([]byte{0x00}, newDomain, o.solution.Hash)
	// log.Printf("newScript: %x\n", *newScript)
	// log.Printf("restateScript: %x\n", *restateScript)
	tx.AddOutput(&transaction.TransactionOutput{
		LockingScript: newScript,
		Satoshis:      1,
	})
	tx.AddOutput(&transaction.TransactionOutput{
		LockingScript: o.BuildInscription(newDomain, ownerScript),
		Satoshis:      1,
	})
	return tx, nil
}

func (o *Opns) BuildInscription(domain string, ownerScript *script.Script) *script.Script {
	lockingScript := script.NewFromBytes(*ownerScript)
	lockingScript.AppendOpcodes(script.OpFALSE, script.OpIF)
	lockingScript.AppendPushData([]byte("ord"))
	lockingScript.AppendOpcodes(script.Op1)
	lockingScript.AppendPushData([]byte("application/op-ns"))
	lockingScript.AppendOpcodes(script.Op0)
	lockingScript.AppendPushData([]byte(domain))
	lockingScript.AppendOpcodes(script.OpENDIF, script.OpRETURN)
	lockingScript.AppendPushData([]byte("1opNSUJVbBc2Vf8LFNSoywGGK4jMcGVrC"))
	lockingScript.AppendPushData(genesis)
	return lockingScript
}

func Lock(claimed []byte, domain string, pow []byte) *script.Script {
	state := script.NewFromBytes([]byte{})
	state.AppendOpcodes(script.OpRETURN, script.OpFALSE)
	state.AppendPushData(genesis)
	state.AppendPushData(claimed)
	state.AppendPushData([]byte(domain))
	state.AppendPushData(pow)
	stateSize := uint32(len(*state) - 1)
	stateScript := binary.LittleEndian.AppendUint32(*state, stateSize)
	stateScript = append(stateScript, 0x00)

	s := make([]byte, len(contract)+len(stateScript))
	copy(s, contract)
	copy(s[len(contract):], stateScript)
	lockingScript := script.NewFromBytes(s)
	return lockingScript
}

func (o *Opns) Unlock(char byte, ownerScript *script.Script) (*OpnsUnlocker, error) {
	unlock := &OpnsUnlocker{
		Opns:        *o,
		Char:        char,
		OwnerScript: ownerScript,
	}
	return unlock, nil
}

func (o *Opns) Mine(char byte) *Pow {
	CONCURRENCY := runtime.NumCPU()
	limit := make(chan struct{}, CONCURRENCY)
	done := make(chan *Pow)
	counter := uint(0)
	for {
		select {
		case nonce := <-done:
			return nonce
		default:
			limit <- struct{}{}
			go func() {
				test := append([]byte{}, o.Pow...)
				test = append(test, char)
				nonce := make([]byte, 32)
				counter++
				rand.Read(nonce)
				// nonce, _ := hex.DecodeString("3ffd296edebfae7f")
				test = append(test, nonce...)

				hash := sha256.Sum256(test)
				hash = sha256.Sum256(hash[:])

				testInt := new(big.Int).SetBytes(util.ReverseBytes(hash[:]))
				testInt = testInt.Rsh(testInt, uint(256-DIFFICULTY))
				<-limit
				if testInt.Cmp(comp) == 0 {
					fmt.Printf("Test: %b %x\n", testInt, util.ReverseBytes(hash[:]))
					fmt.Printf("Found: %x\n", nonce)
					done <- &Pow{
						Nonce:  nonce,
						Hash:   hash[:],
						Hashes: uint64(counter),
					}

				}
			}()
		}
	}
}

func (o *OpnsUnlocker) Sign(tx *transaction.Transaction, inputIndex uint32) (*script.Script, error) {
	unlockScript := &script.Script{}

	// pow := o.Mine(o.Char)
	unlockScript.AppendPushData([]byte{o.Char})
	unlockScript.AppendPushData([]byte(o.solution.Nonce))
	unlockScript.AppendPushData(*o.OwnerScript)
	trailingOutputs := []byte{}
	if len(tx.Outputs) > 3 {
		for _, output := range tx.Outputs[3:] {
			trailingOutputs = append(trailingOutputs, output.Bytes()...)
		}
	}
	unlockScript.AppendPushData(trailingOutputs)
	if preimage, err := tx.CalcInputPreimage(inputIndex, sighash.All|sighash.AnyOneCanPayForkID); err != nil {
		return nil, err
	} else {
		unlockScript.AppendPushData(preimage)
	}
	return unlockScript, nil
}

func (o *OpnsUnlocker) EstimateLength(tx *transaction.Transaction, inputIndex uint32) uint32 {
	trailingOutputs := []byte{}
	if len(tx.Outputs) > 2 {
		for _, output := range tx.Outputs[2:] {
			trailingOutputs = append(trailingOutputs, output.Bytes()...)
		}
	}
	toPrefix, _ := script.PushDataPrefix(trailingOutputs)
	osPrefix, _ := script.PushDataPrefix(*o.OwnerScript)
	preimage, _ := tx.CalcInputPreimage(inputIndex, sighash.AnyOneCanPayForkID)
	preimagePrefix, _ := script.PushDataPrefix(preimage)

	return uint32(len(contract) +
		4 + // OP_RETURN isGenesis push char
		33 + // push data nonce
		len(osPrefix) + len(*o.OwnerScript) + // push data ownerScript
		len(toPrefix) + len(trailingOutputs) + // push data trailingOutputs
		len(preimagePrefix) + len(preimage)) // push data preimage

}
