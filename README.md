![passing build](https://github.com//livematrix/server/actions/workflows/go.yml/badge.svg)

# livematrix server
Simple server to create Live Chat powered by the Matrix.org Protocol


This repository is using github actions, automatic compiled releases are posted to the showcase [repo](https://github.com/osousa/livematrix/) at "_server" folder. 

To test locally, edit **.env.dev** and run: 

```
go run . -dev
```

For now, you can suppress terminal logs with:
```
./livematrix 2>/dev/null &
```

The only build within the Makefile is for linux, if you want other ones, open an issue, i'll add it. 
