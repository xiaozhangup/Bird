package main

import (
	"runtime/debug"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

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
	BackupFiles  []string          `json:"backup_files"`
}

type BackupFileDriverFactory struct {
	RootPath      string
	WatchedFiles  []string
	Perm          server.Perm
	InstanceLabel string
}

func (factory *BackupFileDriverFactory) NewDriver() (server.Driver, error) {
	base := &filedriver.FileDriver{RootPath: factory.RootPath, Perm: factory.Perm}

	watchedRealPaths := normalizeWatchedRealPaths(factory.RootPath, factory.WatchedFiles)
	return &BackupFileDriver{
		base:             base,
		watchedRealPaths: watchedRealPaths,
		enabled:          len(watchedRealPaths) > 0,
		instanceLabel:    factory.InstanceLabel,
	}, nil
}

type BackupFileDriver struct {
	base             *filedriver.FileDriver
	watchedRealPaths map[string]struct{}
	enabled          bool
	instanceLabel    string
}

func normalizeWatchedRealPaths(rootPath string, watchedFiles []string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, watchedFile := range watchedFiles {
		trimmed := strings.TrimSpace(watchedFile)
		if trimmed == "" {
			continue
		}

		ftpPath := trimmed
		if !strings.HasPrefix(ftpPath, "/") {
			ftpPath = "/" + ftpPath
		}

		realPath := realPathFromFTPPath(rootPath, ftpPath)
		absPath, err := filepath.Abs(realPath)
		if err != nil {
			result[filepath.Clean(realPath)] = struct{}{}
			continue
		}
		result[filepath.Clean(absPath)] = struct{}{}
	}

	return result
}

func realPathFromFTPPath(rootPath, path string) string {
	segments := strings.Split(path, "/")
	return filepath.Join(append([]string{rootPath}, segments...)...)
}

func backupPathBeforeWrite(originalPath string) (string, error) {
	dir := filepath.Dir(originalPath)
	backupDir := filepath.Join(dir, "backup")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", err
	}

	baseName := filepath.Base(originalPath)
	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)
	timestamp := time.Now().Format("2006-01-02 15-04-05")

	candidate := filepath.Join(backupDir, fmt.Sprintf("%s (%s)%s", nameWithoutExt, timestamp, ext))
	if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
		return candidate, nil
	}

	for i := 1; i <= 9999; i++ {
		candidate = filepath.Join(backupDir, fmt.Sprintf("%s (%s %s)%s", nameWithoutExt, timestamp, strconv.Itoa(i), ext))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("unable to allocate backup file name for %s", originalPath)
}

func copyFile(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}

	return nil
}

func (d *BackupFileDriver) Init(conn *server.Conn) {
	d.base.Init(conn)
}

func (d *BackupFileDriver) Stat(path string) (server.FileInfo, error) {
	return d.base.Stat(path)
}

func (d *BackupFileDriver) ChangeDir(path string) error {
	return d.base.ChangeDir(path)
}

func (d *BackupFileDriver) ListDir(path string, callback func(server.FileInfo) error) error {
	return d.base.ListDir(path, callback)
}

func (d *BackupFileDriver) DeleteDir(path string) error {
	return d.base.DeleteDir(path)
}

func (d *BackupFileDriver) DeleteFile(path string) error {
	return d.base.DeleteFile(path)
}

func (d *BackupFileDriver) Rename(fromPath string, toPath string) error {
	return d.base.Rename(fromPath, toPath)
}

func (d *BackupFileDriver) MakeDir(path string) error {
	return d.base.MakeDir(path)
}

func (d *BackupFileDriver) GetFile(path string, offset int64) (int64, io.ReadCloser, error) {
	return d.base.GetFile(path, offset)
}

func (d *BackupFileDriver) PutFile(destPath string, data io.Reader, appendData bool) (int64, error) {
	if d.enabled {
		destRealPath := realPathFromFTPPath(d.base.RootPath, destPath)
		destAbsPath, err := filepath.Abs(destRealPath)
		if err != nil {
			destAbsPath = filepath.Clean(destRealPath)
		}

		if _, watched := d.watchedRealPaths[filepath.Clean(destAbsPath)]; watched {
			if st, statErr := os.Stat(destAbsPath); statErr == nil && !st.IsDir() && st.Size() > 0 {
				backupPath, backupErr := backupPathBeforeWrite(destAbsPath)
				if backupErr != nil {
					return 0, backupErr
				}
				if copyErr := copyFile(destAbsPath, backupPath); copyErr != nil {
					return 0, copyErr
				}
				logInfo("[%s] backup created: %s", d.instanceLabel, backupPath)
			}
		}
	}

	return d.base.PutFile(destPath, data, appendData)
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

			backupFiles := c.BackupFiles

			if err := os.MkdirAll(c.Dir, 0755); err != nil {
				logError("[Port %d] Failed to create directory %s: %v", c.Port, c.Dir, err)
				return
			}

			factory := &BackupFileDriverFactory{
				RootPath:      c.Dir,
				WatchedFiles:  backupFiles,
				Perm:          server.NewSimplePerm("ftpuser", "ftpgroup"),
				InstanceLabel: fmt.Sprintf("Port %d", c.Port),
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
			if len(backupFiles) > 0 {
				logInfo("  ├─ Backup")
				var validBackupFiles []string
				for _, watched := range backupFiles {
					if strings.TrimSpace(watched) == "" {
						continue
					}
					validBackupFiles = append(validBackupFiles, watched)
				}
				for i, watched := range validBackupFiles {
					branch := "├─"
					if i == len(validBackupFiles)-1 {
						branch = "└─"
					}
					logInfo("  │  %s %s", branch, watched)
				}
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