// 重建迁移目录的 atlas.sum（删除迁移文件后校准）。
package main

import (
	"fmt"
	"os"

	"ariga.io/atlas/sql/migrate"
)

func main() {
	dir, err := migrate.NewLocalDir(os.Args[1])
	if err != nil {
		panic(err)
	}
	files, err := dir.Files()
	if err != nil {
		panic(err)
	}
	sum, err := migrate.NewHashFile(files)
	if err != nil {
		panic(err)
	}
	if err := migrate.WriteSumFile(dir, sum); err != nil {
		panic(err)
	}
	fmt.Println("sum rewritten:", os.Args[1])
}
