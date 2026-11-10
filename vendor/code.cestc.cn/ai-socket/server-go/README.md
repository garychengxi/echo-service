# Golang微服务框架 on Gin.
提供微服务所需要的HTTP服务，配置文件解析，通用中间件等功能

# Quick Start

# 使用规范


## 目录规范
参考 examples/example-service1
```
|- cmd                          // 程序启动包
    |- server
        server.go               // 服务的启动文件
    |- cli
        cli.go                  // CLI的启动文件
|- conf                         // 配置层，放置配置文件(.toml)
    |- server
        config.toml             // 服务的配置文件
    |- cli
        |- .ctl                 // ctl为具体命令行的名字，该目录仅为示例，将在命令行工具安装执行后自动生成
            |- config.yaml      // 命令行全局配置
|- doc                          // 文档，Swagger文件通过Makefile自动生成到该目录
|- internal                     // 内部业务逻辑
    |- app                      // 服务的逻辑代码
        |- module1              // 按模块划分
            |- controller.go    // 接口接入层，将接口的request/response解析成api中定义的结构体
            |- service.go       // 业务逻辑层
        |- module2
    |- table                    // 数据模型层，定义数据库中所有表结构和基本增删改查方法
|- deploy                       // 部署相关
    |- docker                   // 放Docker镜像构建相关文件，Dockerfile等
        |- Dockerfile
    |- charts                   // 放Helm chart文件
        |- xxx-chart
    |- scripts                  // 放其他脚本文件
|- Makefile
|- ci.yml
```