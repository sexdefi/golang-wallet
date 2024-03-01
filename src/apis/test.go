package apis

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"main/src/rpcs"
	"main/src/utils"
	"net/http"
	"regexp"
)

const transferPath = "^/api/test/([A-Z]{3,})/transfer$"
const miningPath = "^/api/test/([A-Z]{3,})/mining$"

var tstRouteMap = map[string]interface{}{
	fmt.Sprintf("%s %s", http.MethodPost, transferPath): transfer,
	fmt.Sprintf("%s %s", http.MethodPut, miningPath):    doMining,
	fmt.Sprintf("%s %s", http.MethodGet, miningPath):    isMining,
}

type transactionReq struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Amount float64 `json:"amount"`
}

func transfer(w http.ResponseWriter, req *http.Request) []byte {
	var resp RespVO
	re := regexp.MustCompile(transferPath)
	params := re.FindStringSubmatch(req.RequestURI)[1:]
	if len(params) == 0 {
		resp.Code = 500
		resp.Msg = "需要指定币种的名字"
		ret, _ := json.Marshal(resp)
		return ret
	}

	// 参数解析
	var body []byte
	var err error
	if body, err = ioutil.ReadAll(req.Body); err != nil {
		utils.LogMsgEx(utils.WARNING, "解析请求体错误：%v", err)
		resp.Code = 500
		resp.Msg = err.Error()
		ret, _ := json.Marshal(resp)
		return ret
	}
	defer req.Body.Close()

	utils.LogMsgEx(utils.INFO, "收到交易请求：%s", string(body))

	var txReq transactionReq
	if err = json.Unmarshal(body, &txReq); err != nil {
		utils.LogIdxEx(utils.WARNING, 38, err)
		resp.Code = 500
		resp.Msg = err.Error()
		ret, _ := json.Marshal(resp)
		return ret
	}

	rpc := rpcs.GetRPC(params[0])
	var txHash string
	tradePwd := utils.GetConfig().GetCoinSettings().TradePassword
	if txHash, err = rpc.SendTransaction(txReq.From, txReq.To, txReq.Amount, tradePwd); err != nil {
		utils.LogMsgEx(utils.ERROR, "发送交易失败：%v", err)
		resp.Code = 500
		resp.Msg = err.Error()
		ret, _ := json.Marshal(resp)
		return ret
	}

	resp.Code = 200
	resp.Data = txHash
	ret, _ := json.Marshal(resp)
	return []byte(ret)
}

type miningReq struct {
	Enable bool `json:"enable"`
	Speed  int  `json:"speed"`
}

func doMining(w http.ResponseWriter, req *http.Request) []byte {
	var resp RespVO
	re := regexp.MustCompile(miningPath)
	params := re.FindStringSubmatch(req.RequestURI)[1:]
	if len(params) == 0 {
		resp.Code = 500
		resp.Msg = "需要指定币种的名字"
		ret, _ := json.Marshal(resp)
		return ret
	}

	var body []byte
	var err error
	if body, err = ioutil.ReadAll(req.Body); err != nil {
		utils.LogMsgEx(utils.WARNING, "解析请求体错误：%v", err)
		resp.Code = 500
		resp.Msg = err.Error()
		ret, _ := json.Marshal(resp)
		return ret
	}
	defer req.Body.Close()

	var mining miningReq
	if err = json.Unmarshal(body, &mining); err != nil {
		utils.LogIdxEx(utils.WARNING, 38, err)
		resp.Code = 500
		resp.Msg = err.Error()
		ret, _ := json.Marshal(resp)
		return ret
	}
	miningSpeed := 1
	if mining.Speed > 1 {
		miningSpeed = mining.Speed
	}
	rpc := rpcs.GetRPC(params[0])
	if res, err := rpc.EnableMining(mining.Enable, miningSpeed); err != nil {
		utils.LogMsgEx(utils.WARNING, "调整挖矿状态失败：%v", err)
		resp.Code = 500
		resp.Msg = err.Error()
		ret, _ := json.Marshal(resp)
		return ret
	} else {
		resp.Code = 200
		resp.Data = res
		ret, _ := json.Marshal(resp)
		return []byte(ret)
	}
	resp.Code = 200
	ret, _ := json.Marshal(resp)
	return []byte(ret)
}

func isMining(w http.ResponseWriter, req *http.Request) []byte {
	var resp RespVO
	resp.Code = 200
	resp.Data = rpcs.GetRPC(utils.GetConfig().GetCoinSettings().Name).IsMining()
	ret, _ := json.Marshal(resp)
	return []byte(ret)
}
