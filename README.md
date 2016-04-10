# backup-rsync
Wrapper for rsync to make periodic incremental filesystem backups

# Example
Put on backup machine config file /etc/backup-rsync.yml:
```
---
root_dir: "/home/backup"
concurrent_rsync: 3
retention_days: 7
hosts:
- name: host1
  dirs:
  - path: "/path/to/backup_dir"
  
- name: host2
  retention_days: 24
  dirs:
  - path: "/path/to/backup_dir1"
    retention_days: 12
    bandwidth_limit: 1000 # in KB/s
  - path: "/path/to/backup_dir2"
  
- name: host3
  login_user: user3
  dirs:
  - path: "/path/to/backup_dir1"
    retention_days: 32
  - path: "/path/to/backup_dir3"
    bandwidth_limit: 100 # in KB/s
```

There'll be created backups in /home/backup directory:
```
/home/backup/
├── host1
│   └── backup_dir
│       ├── 2016-04-08
│       ├── 2016-04-09
│       ├── 2016-04-10
│       └── current
├── host2
│   ├── backup_dir1
│   │   ├── 2016-04-08
│   │   ├── 2016-04-09
│   │   ├── 2016-04-10
│   │   └── current
│   └── backup_dir2
│       ├── 2016-04-08
│       ├── 2016-04-09
│       ├── 2016-04-10
│       └── current
└── host3
    ├── backup_dir1
    │   ├── 2016-04-08
    │   ├── 2016-04-09
    │   ├── 2016-04-10
    │   └── current
    └── backup_dir2
        ├── 2016-04-08
        ├── 2016-04-09
        ├── 2016-04-10
        └── current
```
