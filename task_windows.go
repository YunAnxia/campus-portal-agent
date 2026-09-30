//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const taskName = "CampusPortalAgent"

func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

func buildTaskXML(exe, workDir string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>校园网门户自动重登保活代理。仅用于本机自身账号保活。</Description>
  </RegistrationInfo>
  <Triggers>
    <BootTrigger>
      <Enabled>true</Enabled>
      <Delay>PT30S</Delay>
    </BootTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>S-1-5-18</UserId>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Enabled>true</Enabled>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>run -dir "%s"</Arguments>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`, xmlEscape(exe), xmlEscape(workDir), xmlEscape(workDir))
}

// registerTask 以 SYSTEM 身份注册开机启动的计划任务。
// 以 SYSTEM 运行的原因：需要"无人登录时也保持认证"，且避免在任务中保存用户密码。
func registerTask(exe, workDir string) error {
	xml := buildTaskXML(exe, workDir)
	xmlPath := filepath.Join(workDir, taskName+".xml")
	if err := os.WriteFile(xmlPath, []byte(xml), 0o644); err != nil {
		return fmt.Errorf("写入任务 XML 失败: %w", err)
	}

	out, err := exec.Command("schtasks.exe", "/Create", "/TN", taskName, "/XML", xmlPath, "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks 创建任务失败: %v\n输出: %s\n已生成任务 XML，可在「任务计划程序」中手动导入:\n  %s",
			err, strings.TrimSpace(string(out)), xmlPath)
	}
	return nil
}

func unregisterTask() error {
	out, err := exec.Command("schtasks.exe", "/Delete", "/TN", taskName, "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks 删除任务失败: %v\n输出: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func queryTask() string {
	out, _ := exec.Command("schtasks.exe", "/Query", "/TN", taskName, "/FO", "LIST").CombinedOutput()
	return string(out)
}
