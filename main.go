/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package main

import (
	"txt2geo/cmd"
)

func main() {
	cmd.Execute()
}

// go build -ldflags="-s -w -X 'txt2geo/internal/version.Version=v1.0.0' -X 'txt2geo/internal/version.Commit=$(git rev-parse HEAD)' -X 'txt2geo/internal/version.BuildDate=$(Get-Date -Format 'yyyy-MM-dd_HH:mm:ss')'" -o release\TXT2GEO.exe.
// upx --best --compress-resources=0 TXT2GEO.exe
