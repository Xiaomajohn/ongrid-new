#!/usr/bin/expect -f
# 通过 expect 自动登录 ssh 并执行命令
set timeout 30
set host [lindex $argv 0]
set cmd  [lindex $argv 1]
set pw   "111111"

spawn ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@$host
expect {
    -re "(yes/no|fingerprint)" { send "yes\r"; exp_continue }
    -re "password:"            { send "$pw\r"; exp_continue }
    -re "\[?\]? *\$"            { send "$cmd\r" }
}
expect {
    -re "\[?\]? *\$" { send "exit\r" }
    timeout          { send "exit\r" }
}
expect eof