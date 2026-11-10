# 使用说明
- logger是在uber-go/zap库的基础上开发的。
- 以下所有使用示例都在[logger/examples/](./examples)目录中

### 默认logger
logger中在init的时候，会生成一个默认logger，如果无需特殊需求，可以开箱即用。
参考：[example_test.go](./examples/example_test.go)中的DefaultLogger方法

### 自定义logger

##### 1. 根据struct配置初始化logger
参考：[example_test.go](./examples/example_test.go)中的CustomLogger方法

##### 2. 根据toml文件初始化logger
配置文件说明参考[logger.toml](examples/conf/logger.toml)。
参考：[example_test.go](./examples/example_test.go)中的CustomLoggerWithConfigFile方法
    
### Fields
日志数据可以携带特殊的field，有利于对日志的分析统计。
参考：[example_test.go](./examples/example_test.go)中的AddGlobalFields方法
