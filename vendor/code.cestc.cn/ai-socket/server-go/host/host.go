package host

type LifeCycle interface {
	// StartAsync 异步的启动，无阻塞操作。建议每个service在这个函数中将接口路由注册到HttpServer上，可以接受请求
	StartAsync()
	// StopAsync 异步的停止，无阻塞操作。
	StopAsync()
}

type Host interface {
	AddService(services ...LifeCycle)
	Run()
}
