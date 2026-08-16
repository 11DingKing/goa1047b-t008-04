# Bug Reproduction

## 包的性质

当前 test_model_fix 保存的是被测模型修复后的结果源码，不是初始含 Bug 源码。要复现原始缺陷，必须检出下面固定的 parent SHA；不要在当前修复结果源码上期待重新出现修复前失败。生成系统使用的可信验证补丁和完整验证日志仅在本地留存，不提交到结果分支。

## 问题现象

帮我查一个整个服务卡死的问题。先不要改代码，我需要先拿到准确的根因和证据再决定怎么动。

现象：

1. 服务跑一段时间后，所有业务接口都不再返回：/api/slots、/api/windows、/api/reservations/{id}、锁舱、装船清单核验、冻结和解冻，请求发过去就一直挂着，不返回结果、不报错、也不超时，最后是客户端自己断开。
2. GET /api/health 始终正常返回 200，进程也没退出，没有 panic，看起来服务是活的。
3. 日志的最后一段是一条 [NOTIFY] ... slot auto-released: cargo not arrived 24h before cutoff ... 之后就再也没有任何 auto-released 或 temperature alerts 输出，后台巡检像是彻底停了。
4. 触发点很确定：只要有一票货到截关前 24 小时还没到场（按规则这时候该自动释放它的舱位、把运力让给等待队列的下一位），那一次巡检之后服务就卡死。如果所有货都按时到场，服务可以一直正常跑。
5. 那个窗口的等待队列有人排队还是空着都一样会卡。
6. 重启服务立刻恢复正常，直到下一次又有货没按时到场。
7. 卡死前后数据看起来没被写坏：被释放的那条预约状态确实变成了 released，舱位的 booked 也减了。

复现：建一个截关时间已经进入 24 小时以内的窗口和满舱的舱位，挂一条已锁舱但货一直没到场的预约，跑一次自动释放巡检，然后再发任意一个业务接口请求，看它还会不会返回。

请定位这个「一次自动释放之后整个服务不再响应」的根因：说明是哪个 Go 文件里的哪个符号、它的什么错误行为，以及这个错误行为为什么会造成上面这些症状（包括为什么健康检查还正常、为什么没有发生自动释放时不会卡、为什么日志停在那条通知之后、为什么重启能恢复）。先给结论和证据，不要改仓库里的代码。

## 含 Bug 版本

- 仓库：11DingKing/goa1047b-t008-04
- 仓库地址：https://github.com/11DingKing/goa1047b-t008-04.git
- parent SHA：7342ed7cf91bad0d5265e2d3bc5b8035094c99b4

## 复现步骤

```bash
git clone -- https://github.com/11DingKing/goa1047b-t008-04.git bug-repro
cd bug-repro
git checkout --detach 7342ed7cf91bad0d5265e2d3bc5b8035094c99b4
go test -timeout=120s ./internal/transport/ -run "TestHTTP_AutoReleaseSweepFreesCapacityAndKeepsServing|TestHTTP_AutoReleaseSweepWithoutWaitlistKeepsServing|TestHTTP_RepeatedAutoReleaseSweepsKeepServing|TestHTTP_AutoReleaseSweepLeavesArrivedCargoAlone" -count=1 -v
```

## 双架构完整错误信息

### linux/amd64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test -timeout=120s ./internal/transport/ -run "TestHTTP_AutoReleaseSweepFreesCapacityAndKeepsServing|TestHTTP_AutoReleaseSweepWithoutWaitlistKeepsServing|TestHTTP_RepeatedAutoReleaseSweepsKeepServing|TestHTTP_AutoReleaseSweepLeavesArrivedCargoAlone" -count=1 -v
=== RUN   TestHTTP_AutoReleaseSweepFreesCapacityAndKeepsServing
    auto_release_sweep_test.go:141: auto-release sweep did not finish within 5s
--- FAIL: TestHTTP_AutoReleaseSweepFreesCapacityAndKeepsServing (5.01s)
=== RUN   TestHTTP_AutoReleaseSweepWithoutWaitlistKeepsServing
    auto_release_sweep_test.go:178: auto-release sweep did not finish within 5s
--- FAIL: TestHTTP_AutoReleaseSweepWithoutWaitlistKeepsServing (5.00s)
=== RUN   TestHTTP_RepeatedAutoReleaseSweepsKeepServing
    auto_release_sweep_test.go:204: auto-release sweep did not finish within 5s
--- FAIL: TestHTTP_RepeatedAutoReleaseSweepsKeepServing (5.01s)
=== RUN   TestHTTP_AutoReleaseSweepLeavesArrivedCargoAlone
--- PASS: TestHTTP_AutoReleaseSweepLeavesArrivedCargoAlone (0.01s)
FAIL
FAIL	github.com/arctic-express/scheduler/internal/transport	15.074s
FAIL

```

stderr：

```text
(empty)
```

### linux/arm64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test -timeout=120s ./internal/transport/ -run "TestHTTP_AutoReleaseSweepFreesCapacityAndKeepsServing|TestHTTP_AutoReleaseSweepWithoutWaitlistKeepsServing|TestHTTP_RepeatedAutoReleaseSweepsKeepServing|TestHTTP_AutoReleaseSweepLeavesArrivedCargoAlone" -count=1 -v
=== RUN   TestHTTP_AutoReleaseSweepFreesCapacityAndKeepsServing
    auto_release_sweep_test.go:141: auto-release sweep did not finish within 5s
--- FAIL: TestHTTP_AutoReleaseSweepFreesCapacityAndKeepsServing (5.00s)
=== RUN   TestHTTP_AutoReleaseSweepWithoutWaitlistKeepsServing
    auto_release_sweep_test.go:178: auto-release sweep did not finish within 5s
--- FAIL: TestHTTP_AutoReleaseSweepWithoutWaitlistKeepsServing (5.00s)
=== RUN   TestHTTP_RepeatedAutoReleaseSweepsKeepServing
    auto_release_sweep_test.go:204: auto-release sweep did not finish within 5s
--- FAIL: TestHTTP_RepeatedAutoReleaseSweepsKeepServing (5.00s)
=== RUN   TestHTTP_AutoReleaseSweepLeavesArrivedCargoAlone
--- PASS: TestHTTP_AutoReleaseSweepLeavesArrivedCargoAlone (0.00s)
FAIL
FAIL	github.com/arctic-express/scheduler/internal/transport	15.013s
FAIL

```

stderr：

```text
(empty)
```

## 通过条件

通过标准（diagnosis）：
1. 命中 gold 根因涉及的文件：internal/service/delivery.go
2. 命中 gold 根因涉及的符号：(*DeliveryService).CheckAutoRelease
3. 命中正确的失效机制：该方法在已经持有 store.Store.Mu 的情况下，对每条被释放的预约调用了会自行加同一把锁的导出方法 (*DeliveryService).PromoteWaitlist，而 sync.Mutex 不可重入，于是同一 goroutine 第二次 Lock 永久阻塞并永久持锁；由于 store 的访问器按设计不自锁、所有业务入口都先锁 Mu，整个服务的业务链路随之全部阻塞，而不碰 store 的健康检查不受影响。应当调用同文件中要求调用方已持锁的 promoteWaitlistLocked
4. 结论有实际证据（读过相关代码或跑过复现），不是凭空推断
5. 目标仓库全程零改动；容器内一次性独立复现程序不计为项目代码改动
6. 复现依据：
   go test -timeout=120s ./internal/transport/ -run 'TestHTTP_AutoReleaseSweepFreesCapacityAndKeepsServing|TestHTTP_AutoReleaseSweepWithoutWaitlistKeepsServing|TestHTTP_RepeatedAutoReleaseSweepsKeepServing|TestHTTP_AutoReleaseSweepLeavesArrivedCargoAlone' -count=1 -v
   在 main 上失败、在 gold_model_fix 上通过
