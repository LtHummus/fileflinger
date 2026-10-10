package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/lthummus/fileflinger/internal/fdsend"
	"github.com/urfave/cli/v3"
)

func testCommand() *cli.Command {
	return &cli.Command{
		Name:   "test",
		Hidden: true,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			socketPath := filepath.Join(os.TempDir(), "fd-test.sock")
			_ = os.Remove(socketPath)

			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
			if err != nil {
				panic(err)
			}
			defer listener.Close()
			defer os.Remove(socketPath)

			f, err := os.Open("main.go")
			if err != nil {
				panic(err)
			}
			defer f.Close()

			fmt.Printf("Opened main.go for sending (sender FD = %d)....\n", f.Fd())

			go func() {
				time.Sleep(50 * time.Millisecond)

				fmt.Printf("[Sender] connecting to socket....\n")
				conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
				if err != nil {
					panic(err)
				}
				defer conn.Close()

				fmt.Printf("[Sender] sending file....\n")
				if err := fdsend.SendFD(conn, f, "somekey"); err != nil {
					panic(err)
				}
				fmt.Printf("[Sender] Sent!\n")
			}()

			fmt.Printf("[Recv] Waiting for connection...\n")
			conn, err := listener.AcceptUnix()
			if err != nil {
				panic(err)
			}
			defer conn.Close()

			fmt.Printf("[Recv] Connection accepted...\n")

			f, key, err := fdsend.RecvFD(conn)
			if err != nil {
				panic(err)
			}

			fmt.Printf("[Recv] Got file handle. Number: %d\n", f.Fd())
			defer f.Close()

			if _, err := conn.Write(fdsend.SocketMsgOk); err != nil {
				panic(err)
			}

			buf, err := io.ReadAll(f)
			if err != nil {
				panic(err)
			}

			fmt.Printf("[Recv] Read file. %d bytes\n", len(buf))

			fmt.Printf("[Recv] Got key: %s\n", key)

			return nil
		},
	}
}
