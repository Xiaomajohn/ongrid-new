module gitlab-issue

go 1.23

// 无第三方依赖;binary 用 stdlib 跑 stdio JSON-RPC 协议即可完成 demo。
// 真生产化阶段(plugin 准备接 GitLab API + vault secret)再视情况加
// github.com/xanzy/go-gitlab 等。
