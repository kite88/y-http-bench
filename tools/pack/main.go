// Command pack 把单个可执行文件打成 .tar.gz 或 .zip，并显式写入归档内的文件权限。
//
// 为什么不用系统工具：
//   - Windows 自带的 tar.exe 是精简版 bsdtar：它不读文件权限（Windows 上没有 Unix
//     执行位这个概念，它一律伪造 0666），也不支持 --mode，打出来的包在 Linux 上
//     解压后是 644、根本执行不了；
//   - zip(1) 在 macOS / Linux 上并非必然存在，不同实现写出的字节也不一致。
//
// Go 标准库可以显式设置权限、并把时间戳固定下来，因此无论在哪个平台、用
// build.ps1 还是 build.sh，打出的包都完全一致（可复现、校验和相同）。
// 格式由 -out 的扩展名决定：.zip 出 zip，其余出 tar.gz。
//
// 用法（由构建脚本调用，一般不需要手动执行）：
//
//	pack -in dist/y-http-bench -out dist/y-http-bench-linux-amd64.tar.gz -name y-http-bench -mode 0755
//	pack -in dist/y-http-bench.exe -out dist/y-http-bench-windows-amd64.zip -name y-http-bench.exe
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	in := flag.String("in", "", "输入文件（必填）")
	out := flag.String("out", "", "输出路径，.zip 或 .tar.gz（必填）")
	name := flag.String("name", "", "归档内的文件名，默认取输入文件名")
	mode := flag.String("mode", "0755", "归档内的文件权限，八进制，默认 0755")
	flag.Parse()

	if err := pack(*in, *out, *name, *mode); err != nil {
		fmt.Fprintln(os.Stderr, "pack:", err)
		os.Exit(1)
	}
}

func pack(in, out, name, modeStr string) error {
	if in == "" || out == "" {
		return fmt.Errorf("-in 与 -out 均为必填")
	}
	mode, err := strconv.ParseInt(modeStr, 8, 32)
	if err != nil {
		return fmt.Errorf("解析 -mode 失败: %w", err)
	}
	if name == "" {
		name = filepath.Base(in)
	}

	src, err := os.Open(in)
	if err != nil {
		return err
	}
	defer src.Close()

	fi, err := src.Stat()
	if err != nil {
		return err
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	if strings.EqualFold(filepath.Ext(out), ".zip") {
		err = writeZip(f, src, name, mode)
	} else {
		err = writeTarGz(f, src, fi.Size(), name, mode)
	}
	if err != nil {
		f.Close()
		os.Remove(out) // 失败时别留下半个包
		return err
	}
	return f.Close()
}

// writeTarGz 写 .tar.gz。时间戳固定为 Unix 0 点：源码不变时打出的包完全一致，
// 因此在 tar -tvf 里看到 1970-01-01 是有意为之。
func writeTarGz(w io.Writer, src io.Reader, size int64, name string, mode int64) error {
	gz := gzip.NewWriter(w) // ModTime 同样留零值
	tw := tar.NewWriter(gz)

	// Uid/Gid/Uname/Gname 不设置：避免把打包机的用户身份写进归档
	hdr := &tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     mode,
		Size:     size,
		ModTime:  time.Unix(0, 0),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := io.Copy(tw, src); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// writeZip 写 .zip。SetMode 会同时设置「version made by = Unix」和外置属性，
// 这样解压工具才会认权限位。
func writeZip(w io.Writer, src io.Reader, name string, mode int64) error {
	zw := zip.NewWriter(w)

	hdr := &zip.FileHeader{
		Name:   name,
		Method: zip.Deflate,
		// 时间戳固定为 zip 的纪元（1980-01-01）：DOS 时间字段留 0 时，
		// 不少工具会显示成 1979-11-30，写死更清楚，同样保证可复现。
		Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	hdr.SetMode(fs.FileMode(mode))

	fw, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, src); err != nil {
		return err
	}
	return zw.Close()
}
