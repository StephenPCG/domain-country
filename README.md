Scripts to parse domains' NS country.

Get domain list from dns server log. For example, from dnsmasq log:
```
zcat /var/log/dnsmasq/dnsmasq.log.*.gz | awk '/query.+ from/{print $6}' | sort | uniq > names.txt
```

Build and run resolv-ns-country.go:
```
go build -o resolv-ns-country
cat names.txt | ./resolv-ns-country > names-country.txt
```

Extract CN domains:
```
cat names.txt \
        | awk -F\| '{if($4=="CN"){print $1}}' \
        | tr '[:upper:]' '[:lower:]' \
        | grep -vE '\.cn' \
        | sort \
        | uniq \
        > cnnames.txt
```

Merge other sources:
```
cat \
        <(echo cn) \
        cnnames.txt \
        ... \
        | sort \
        | uniq \
        > final.txt
```

Create dnsmasq conf:
```
SERVER=114.114.114.114
cat china-names.txt | sed "s|\(.*\)|server=/\1/${SERVER}|" > china-names.conf
```
