# build.ps1 - 编译并压缩 GoTXT2GEO 项目

# --- 配置 ---
$Version = "v1.0.0" # 在发布新版本时修改这里
$OutputExe = "release\TXT2GEO.exe"
$UpxPath = "release\upx.exe" # 新增：指定 UPX 的路径

# --- 开始构建 ---
Write-Host "🚀 开始构建版本: $Version..."

# 1. 编译 Go 程序
# 使用 $LASTEXITCODE 检查命令是否成功
go build -ldflags="-s -w -X 'txt2geo/internal/version.Version=$Version' -X 'txt2geo/internal/version.Commit=$(git rev-parse HEAD)' -X 'txt2geo/internal/version.BuildDate=$(Get-Date -Format 'yyyy-MM-dd_HH:mm:ss')'" -o $OutputExe .

if ($LASTEXITCODE -ne 0) {
    Write-Host "❌ 编译失败！"
    exit 1 # 脚本以错误码退出
}

Write-Host "✅ 编译成功: $OutputExe"

# 2. 检查 UPX 是否存在于指定路径
if (-not (Test-Path $UpxPath)) {
    Write-Host "⚠️ 在 '$UpxPath' 未找到 UPX，跳过压缩步骤。"
    exit 0
}

# 3. 执行 UPX 压缩
Write-Host "📦 正在使用 UPX 进行压缩..."
# 使用 & 调用操作符来执行路径中的程序
& $UpxPath --best --compress-resources=0 $OutputExe

if ($LASTEXITCODE -ne 0) {
    Write-Host "❌ UPX 压缩失败！"
    exit 1
}

Write-Host "🎉 构建和压缩全部完成！"