package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"sync"

	filedriver "github.com/goftp/file-driver"
	"github.com/goftp/server"
)

// ServerConfig 映射配置文件中的单个 FTP 实例配置
type ServerConfig struct {
	Port  int               `json:"port"`
	Dir   string            `json:"dir"`
	Users map[string]string `json:"users"`
}

// MultiUserAuth 自定义多用户认证器
type MultiUserAuth struct {
	Users map[string]string // 存储 用户名 -> 密码
}

// CheckPasswd 实现 server.Auth 接口
func (m *MultiUserAuth) CheckPasswd(username, password string) (bool, error) {
	if expectedPwd, ok := m.Users[username]; ok {
		if expectedPwd == password {
			return true, nil // 密码正确
		}
	}
	return false, nil // 密码错误或用户不存在
}

func main() {
	// 1. 定义命令行参数
	configFile := flag.String("config", "config.json", "配置文件路径")
	flag.Parse()

	// 2. 读取配置文件内容
	data, err := os.ReadFile(*configFile)
	if err != nil {
		log.Fatalf("读取配置文件失败 (%s): %v", *configFile, err)
	}

	// 3. 解析 JSON
	var configs []ServerConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		log.Fatalf("解析配置文件失败: %v\n请检查 JSON 格式是否正确。", err)
	}

	if len(configs) == 0 {
		log.Fatalf("配置文件中没有找到任何服务器配置")
	}

	var wg sync.WaitGroup

	for _, cfg := range configs {
		wg.Add(1)

		go func(c ServerConfig) {
			defer wg.Done()

			// 确保目录存在
			if err := os.MkdirAll(c.Dir, 0755); err != nil {
				log.Printf("[端口 %d] 无法创建FTP根目录 %s: %v", c.Port, c.Dir, err)
				return
			}

			// 设置文件系统驱动 (v1 和 v2 相同)
			factory := &filedriver.FileDriverFactory{
				RootPath: c.Dir,
				Perm:     server.NewSimplePerm("ftpuser", "ftpgroup"),
			}

			// 配置 FTP 服务器选项 (★ 区别1：v1 叫 ServerOpts，驱动参数叫 Factory)
			opts := &server.ServerOpts{
				Name:    "My Configured Go FTP Server",
				Factory: factory, 
				Auth:    &MultiUserAuth{Users: c.Users},
				Port:    c.Port,
			}

			// 初始化服务器 (★ 区别2：v1 版本的 NewServer 只返回一个对象，不返回错误)
			ftpServer := server.NewServer(opts)

			var userList []string
			for u := range c.Users {
				userList = append(userList, u)
			}

			log.Printf("=> [启动成功] FTP 实例监听端口: %d | 目录: %s | 用户: %v", c.Port, c.Dir, userList)

			// 启动监听
			if err := ftpServer.ListenAndServe(); err != nil {
				log.Printf("[端口 %d] FTP服务器运行出错退出: %v", c.Port, err)
			}
		}(cfg)
	}

	log.Println("=================================================")
	log.Println("所有配置的 FTP 实例已触发启动，按 Ctrl+C 结束程序")
	log.Println("=================================================")

	wg.Wait()
}