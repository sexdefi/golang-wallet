package rpcs

import (
	"main/src/entities"
	"reflect"
)

type Rpc interface {
	GetTransactions(height uint) ([]entities.Transaction, error)
	GetCurrentHeight() (uint64, error)
	GetDepositAmount() (map[string]float64, error)
	GetBalance(address string) (float64, error)
	SendTransaction(from string, to string, amount float64, password string) (string, error)
	SendFrom(from string, amount float64) (string, error)
	SendTo(to string, amount float64) (string, error)
	GetNewAddress() (string, error)
	ValidAddress(address string) (bool, error)
	GetTransaction(txHash string) ([]entities.Transaction, error)
	GetTxExistsHeight(txHash string) (uint64, error)
	EnableMining(enable bool, speed int) (bool, error)
	IsMining() bool
}

type rpc struct {
}

var __rpc = new(rpc)

func GetRPC(name string) Rpc {
	return reflect.ValueOf(__rpc).MethodByName(name).Call(nil)[0].Interface().(Rpc)
}

type RequestBody struct {
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
	Id     string        `json:"id"`
}
