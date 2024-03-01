package services

import (
	"main/src/dao"
	"main/src/entities"
	"main/src/rpcs"
	"main/src/utils"
	"sync"
	"time"
)

/*
**
提币服务：接收来自API的提币操作，并启动子协程等待其入链后交给通知服务
子协程（waitForWithdraw）：等待从API发来的提币请求
子协程（sendTransactions）：发送列队中的提币请求
子协程（waitForInchain）：查询发起的提币交易，检查是否入链
*/
type withdrawService struct {
	BaseService
	sync.Once
	wdsToSend        []entities.BaseWithdraw
	sendDelayList    map[int]int
	wdsToSendLock    *sync.RWMutex
	wdsToInchain     map[string]uint
	wdsToInchainLock *sync.RWMutex
}

var _withdrawService *withdrawService

func GetWithdrawService() *withdrawService {
	if _withdrawService == nil {
		_withdrawService = new(withdrawService)
		_withdrawService.Once = sync.Once{}
		_withdrawService.Once.Do(func() {
			_withdrawService.create()
		})
	}
	return _withdrawService
}

func (service *withdrawService) create() error {
	service.name = "withdrawService"
	service.status.RegAsObs(service)
	service.wdsToInchain = make(map[string]uint)
	service.wdsToSendLock = new(sync.RWMutex)
	service.wdsToInchainLock = new(sync.RWMutex)
	service.sendDelayList = make(map[int]int)
	return service.BaseService.create()
}

func (service *withdrawService) BeforeTurn(s *utils.Status, tgtStt int) {
	var err error
	switch tgtStt {
	case INIT:
		utils.LogMsgEx(utils.INFO, "initialization", nil)
		// 加载所有未稳定的提币交易
		if err = service.loadWithdrawUnsent(); err != nil {
			panic(utils.LogMsgEx(utils.ERROR, "加载提币失败：%v", err))
		}
	case START:
		utils.LogMsgEx(utils.INFO, "start", nil)
	}
}

func (service *withdrawService) AfterTurn(s *utils.Status, srcStt int) {
	switch s.Current() {
	case INIT:
		utils.LogMsgEx(utils.INFO, "initialized", nil)
	case START:
		// 启动子协程等待API来的提币请求
		go service.waitForWithdraw()
		// 启动子协程发送请求
		go service.sendTransactions()
		// 启动子协程等待请求交易入链
		go service.waitForInchain()
		utils.LogMsgEx(utils.INFO, "started", nil)
	}
}

func (service *withdrawService) RemoveWithdraw(asset string, id int) {
	for i, wd := range service.wdsToSend {
		if wd.Id == id && wd.Asset == asset {
			service.wdsToSendLock.Lock()
			service.wdsToSend = append(service.wdsToSend[:i], service.wdsToSend[i+1:]...)
			service.wdsToSendLock.Unlock()
			return
		}
	}
	for txid := range service.wdsToInchain {
		if i, err := dao.GetWithdrawDAO().GetWithdrawId(asset, txid); err != nil {
			utils.LogMsgEx(utils.ERROR, "未找到交易id为：%s的操作，错误为：%v", txid, err)
			continue
		} else if i == id {
			service.wdsToInchainLock.Lock()
			delete(service.wdsToInchain, txid)
			service.wdsToInchainLock.Unlock()
			return
		}
	}
}

func (service *withdrawService) loadWithdrawUnsent() error {
	var withdraws []entities.DatabaseWithdraw
	var err error
	asset := utils.GetConfig().GetCoinSettings().Name
	if withdraws, err = dao.GetWithdrawDAO().GetUnfinishWithdraw(asset); err != nil {
		return utils.LogMsgEx(utils.ERROR, "获取未发送的提币交易失败：%v", err)
	}

	for _, withdraw := range withdraws {
		if withdraw.Status < entities.WITHDRAW_SENT {
			service.wdsToSend = append(service.wdsToSend, entities.TurnToBaseWithdraw(&withdraw))
			utils.LogMsgEx(utils.INFO, "加载提币请求进待发送列队：%v", withdraw)
		} else if withdraw.Status < entities.WITHDRAW_INCHAIN {
			service.wdsToInchain[withdraw.TxHash] = 0
			utils.LogMsgEx(utils.INFO, "加载提币请求进待入链列队：%v", withdraw)
		}
	}
	return nil
}

func (service *withdrawService) waitForWithdraw() {
	var err error
	for err == nil && service.status.Current() == START {
		// 等待接收来自API的提币请求
		var withdraw entities.BaseWithdraw
		var ok bool
		if withdraw, ok = <-RevWithdrawSig; !ok {
			break
		}
		utils.LogMsgEx(utils.INFO, "接收到一笔待发送的提币：%v", withdraw)

		// 持久化到数据库
		if _, err = dao.GetWithdrawDAO().RecvNewWithdraw(withdraw); err != nil {
			utils.LogMsgEx(utils.ERROR, "新增提币请求失败：%v", err)
			continue
		}
		if _, err = dao.GetProcessDAO().SaveProcess(&entities.DatabaseProcess{
			BaseProcess: entities.BaseProcess{
				Id:         withdraw.Id,
				Asset:      withdraw.Asset,
				Type:       entities.WITHDRAW,
				Process:    entities.LOAD,
				Cancelable: true,
			},
			LastUpdateTime: time.Now(),
		}); err != nil {
			utils.LogMsgEx(utils.ERROR, "新增提币请求失败：%v", err)
			continue
		}
		utils.LogMsgEx(utils.INFO, "已持久化到数据库：%d", withdraw.Id)

		// 保存到内存
		service.wdsToSendLock.Lock()
		service.wdsToSend = append(service.wdsToSend, withdraw)
		service.wdsToSendLock.Unlock()
		utils.LogMsgEx(utils.INFO, "进入待发送提币列队：%d", withdraw.Id)
	}
	service.status.TurnTo(DESTORY)
}

func (service *withdrawService) sendTransactions() {
	asset := utils.GetConfig().GetCoinSettings().Name
	rpc := rpcs.GetRPC(asset)
	var err error
	for err == nil && service.status.Current() == START {
		service.wdsToSendLock.RLock()
		for i, withdraw := range service.wdsToSend {
			// 判断是否是发送延迟提币
			if n, ok := service.sendDelayList[withdraw.Id]; ok {
				if n > 0 {
					service.sendDelayList[withdraw.Id] = n - 1
					continue
				} else {
					delete(service.sendDelayList, withdraw.Id)
				}
			}

			// 发送提币转账请求
			var txHash string
			if txHash, err = rpc.SendTo(withdraw.Address, withdraw.Amount); err != nil {
				utils.LogMsgEx(utils.ERROR, "发送提币请求失败：%v", err)
				err = nil // 如果发送失败，重复尝试
				continue
			}
			if txHash == "" {
				utils.LogMsgEx(utils.ERROR, "空的交易ID", nil)
				service.sendDelayList[withdraw.Id] = 100000
				continue
			}
			utils.LogMsgEx(utils.INFO, "提币请求已发送，收到交易ID：%s", txHash)

			// 持久化到数据库
			if _, err = dao.GetWithdrawDAO().SentForTxHash(asset, txHash, withdraw.Id); err != nil {
				utils.LogMsgEx(utils.ERROR, "持久化到数据库失败：%v", err)
				continue
			}
			if _, err = dao.GetProcessDAO().SaveProcess(&entities.DatabaseProcess{
				BaseProcess: entities.BaseProcess{
					Id:         withdraw.Id,
					TxHash:     txHash,
					Asset:      withdraw.Asset,
					Type:       entities.WITHDRAW,
					Process:    entities.SENT,
					Cancelable: false,
				},
				LastUpdateTime: time.Now(),
			}); err != nil {
				utils.LogMsgEx(utils.ERROR, "持久化到数据库失败：%v", err)
				continue
			}
			utils.LogMsgEx(utils.INFO, "交易：%s已持久化", txHash)

			// 插入待入链列表
			service.wdsToInchainLock.Lock()
			service.wdsToInchain[txHash] = 0
			service.wdsToInchainLock.Unlock()

			// 如果发送成功，立刻删除这笔提币请求（以防重复发提币，为了防止索引出错，立即跳出循环）
			service.wdsToSend = append(service.wdsToSend[:i], service.wdsToSend[i+1:]...)
			break
		}
		service.wdsToSendLock.RUnlock()
	}
	service.status.TurnTo(DESTORY)
}

func (service *withdrawService) waitForInchain() {
	asset := utils.GetConfig().GetCoinSettings().Name
	rpc := rpcs.GetRPC(asset)
	var err error
	for err == nil && service.status.Current() == START {
		service.wdsToInchainLock.RLock()
		for txHash, num := range service.wdsToInchain {
			// 检查交易的块高
			var height uint64
			if height, err = rpc.GetTxExistsHeight(txHash); err != nil {
				utils.LogMsgEx(utils.ERROR, "交易：%s查询错误：%v", txHash, err)
				continue
			}

			// 如果已经入链，发送给notify服务等待稳定
			if height == 0 {
				if num%100 == 0 {
					utils.LogMsgEx(utils.INFO, "交易：%s等待入链", txHash)
				}
				service.wdsToInchain[txHash]++
				continue
			}
			utils.LogMsgEx(utils.INFO, "交易：%s已经入链，高度：%d", txHash, height)

			// 更新状态
			var txs []entities.Transaction
			if txs, err = rpc.GetTransaction(txHash); err != nil {
				utils.LogMsgEx(utils.ERROR, "获取交易：%s失败：%v", txHash, err)
				continue
			}
			for _, wd := range txs {
				if _, err = dao.GetWithdrawDAO().WithdrawIntoChain(asset, txHash, height, wd.TxIndex); err != nil {
					utils.LogMsgEx(utils.ERROR, "持久化到数据库失败：%v", err)
					continue
				}

				var id int
				if id, err = dao.GetWithdrawDAO().GetWithdrawId(asset, txHash); err != nil {
					utils.LogMsgEx(utils.ERROR, "获取提币交易id失败：%v", err)
					continue
				}
				if _, err = dao.GetProcessDAO().SaveProcess(&entities.DatabaseProcess{
					BaseProcess: entities.BaseProcess{
						Id:         id,
						TxHash:     txHash,
						Asset:      wd.Asset,
						Type:       entities.WITHDRAW,
						Process:    entities.INCHAIN,
						Cancelable: false,
					},
					Height:         wd.Height,
					CompleteHeight: wd.Height + uint64(utils.GetConfig().GetCoinSettings().Stable),
					LastUpdateTime: time.Now(),
				}); err != nil {
					utils.LogMsgEx(utils.ERROR, "持久化到数据库失败：%v", err)
					continue
				}
				utils.LogMsgEx(utils.INFO, "交易：%s已持久化", txHash)

				toNotifySig <- wd
			}

			// 同样，一旦发送给待稳定服务，立刻从列表中删除（为了防止索引出错，立即跳出循环）
			delete(service.wdsToInchain, txHash)
			break
		}
		service.wdsToInchainLock.RUnlock()
	}
	service.status.TurnTo(DESTORY)
}
