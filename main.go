package main

import (
	"runtime/debug"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"

	filedriver "github.com/goftp/file-driver"
	"github.com/goftp/server"
)

const (
	colorReset   = "\033[0m"
	colorGreen   = "\033[38;5;118m"
	colorLime    = "\033[38;5;190m"
	colorYellow  = "\033[38;5;226m"
	colorRed     = "\033[38;5;196m"
	colorCyan    = "\033[38;5;51m"
	colorMagenta = "\033[38;5;201m"
	colorGray    = "\033[38;5;245m"
)

const birdBanner = `
     _.-.      ██████  ██ ██████  ██████ 
   /' v '\     ██   ██ ██ ██   ██ ██   ██
  (/     \)    ██████  ██ ██████  ██   ██
 ="="="="="=   ██   ██ ██ ██   ██ ██   ██
   //   \\     ██████  ██ ██   ██ ██████ 
  ^^     ^^      Git Ver: %s
`

var buildRevision = "dev"

type ServerConfig struct {
	Port         int               `json:"port"`
	Dir          string            `json:"dir"`
	Users        map[string]string `json:"users"`
	PublicIP     string            `json:"public_ip"`
	PassivePorts string            `json:"passive_ports"`
}

type MultiUserAuth struct {
	Users map[string]string // 用户名 -> 密码
}

func (m *MultiUserAuth) CheckPasswd(username, password string) (bool, error) {
	if expectedPwd, ok := m.Users[username]; ok {
		if expectedPwd == password {
			return true, nil
		}
	}
	return false, nil
}

func init() {
	log.SetFlags(0)
}

func colorize(color, text string) string {
	return color + text + colorReset
}

func logInfo(format string, args ...any) {
	log.Printf(colorize(colorGreen, "[INFO] ")+" "+format, args...)
}

func logSuccess(format string, args ...any) {
	log.Printf(colorize(colorLime, "[OK]   ")+" "+format, args...)
}

func logWarn(format string, args ...any) {
	log.Printf(colorize(colorYellow, "[WARN] ")+" "+format, args...)
}

func logError(format string, args ...any) {
	log.Printf(colorize(colorRed, "[ERROR]")+" "+format, args...)
}

type FTPLogger struct{}

func (l *FTPLogger) Print(sessionId string, message interface{}) {
	if sessionId == "" {
		logInfo("%v", message)
	} else {
		logInfo("[%s] %v", colorize(colorGray, sessionId), message)
	}
}

func (l *FTPLogger) Printf(sessionId string, format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	if sessionId == "" {
		logInfo("%s", msg)
	} else {
		logInfo("[%s] %s", colorize(colorGray, sessionId), msg)
	}
}

func (l *FTPLogger) PrintCommand(sessionId string, command string, params string) {
	if strings.ToUpper(command) == "PASS" {
		params = "******"
	}
	
	cmdStr := colorize(colorLime, command+" "+params)
	if sessionId == "" {
		logInfo("> %s", cmdStr)
	} else {
		logInfo("[%s] > %s", colorize(colorGray, sessionId), cmdStr)
	}
}

func (l *FTPLogger) PrintResponse(sessionId string, code int, message string) {
	respStr := colorize(colorGreen, fmt.Sprintf("%d %s", code, message))
	if sessionId == "" {
		logInfo("< %s", respStr)
	} else {
		logInfo("[%s] < %s", colorize(colorGray, sessionId), respStr)
	}
}

func printBanner(configFile string, count int) {
	revision := getBuildRevision()
	banner := fmt.Sprintf(birdBanner, revision)

	bannerLines := strings.Split(strings.Trim(banner, "\r\n"), "\n")
	for _, line := range bannerLines {
		log.Println(colorize(colorLime, line))
	}
	log.Println()
	logInfo("Bird FTP Server is starting...")
	logInfo("Build Rev   : %s", colorize(colorMagenta, revision))
	logInfo("Config File : %s", colorize(colorCyan, configFile))
	logInfo("Instances   : %d", count)
	log.Println(colorize(colorGray, strings.Repeat("-", 50)))
}

func getBuildRevision() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				if len(setting.Value) >= 12 {
					return setting.Value[:12]
				}
				return setting.Value
			}
		}
	}

	if buildRevision != "" && buildRevision != "dev" {
		if len(buildRevision) >= 12 {
			return buildRevision[:12]
		}
		return buildRevision
	}

	return "unknown"
}

func main() {
	configFile := flag.String("config", "config.json", "配置文件路径")
	flag.Parse()

	data, err := os.ReadFile(*configFile)
	if err != nil {
		logError("Failed to read config file (%s): %v", *configFile, err)
		os.Exit(1)
	}

	var configs []ServerConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		logError("Failed to parse JSON config: %v", err)
		os.Exit(1)
	}

	if len(configs) == 0 {
		logError("No server configuration found in the config file.")
		os.Exit(1)
	}

	printBanner(*configFile, len(configs))

	var wg sync.WaitGroup

	for _, cfg := range configs {
		wg.Add(1)

		go func(c ServerConfig) {
			defer wg.Done()

			if err := os.MkdirAll(c.Dir, 0755); err != nil {
				logError("[Port %d] Failed to create directory %s: %v", c.Port, c.Dir, err)
				return
			}

			factory := &filedriver.FileDriverFactory{
				RootPath: c.Dir,
				Perm:     server.NewSimplePerm("ftpuser", "ftpgroup"),
			}

			opts := &server.ServerOpts{
				Name:         "Bird FTP Server",
				Factory:      factory,
				Auth:         &MultiUserAuth{Users: c.Users},
				Logger:       &FTPLogger{},
				Port:         c.Port,
				PublicIp:     c.PublicIP,
				PassivePorts: c.PassivePorts,
			}

			ftpServer := server.NewServer(opts)

			var userList []string
			for u := range c.Users {
				userList = append(userList, u)
			}
			sort.Strings(userList)

			logSuccess("Instance running on port %s", colorize(colorYellow, fmt.Sprintf("%d", c.Port)))
			logInfo("  ├─ Directory: %s", c.Dir)
			if c.PublicIP != "" {
				logInfo("  ├─ PublicIP : %s", c.PublicIP)
			}
			if c.PassivePorts != "" {
				logInfo("  ├─ PASV Port: %s", c.PassivePorts)
			}
			logInfo("  └─ Users    : %s", strings.Join(userList, ", "))
			log.Println()

			if err := ftpServer.ListenAndServe(); err != nil {
				logError("[Port %d] Server stopped: %v", c.Port, err)
			}
		}(cfg)
	}

	// 退出程序
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println()
		logWarn("Interrupt signal received. Shutting down Bird FTP Server...")
		os.Exit(0)
	}()

	wg.Wait()
}