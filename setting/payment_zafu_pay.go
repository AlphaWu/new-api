package setting

var (
	ZafuPayAddress  = "https://payment.zafu.edu.cn"
	ZafuPayMyAppId  = ""
	ZafuPayKey      = ""
	ZafuPayMinTopUp = 1
	// ZafuPayDailyLimit 一卡通单日（自然日，服务器本地时区）充值额度上限。
	// 与 ZafuPayMinTopUp 同单位（展示单位），内部与落库 amount 比对前折算为币种单位；
	// 0 表示不限制。
	ZafuPayDailyLimit = 0
)
