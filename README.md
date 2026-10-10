# FileFlinger

FileFlinger is a combination of a daemon, a webserver, and a CLI tool designed for easily sharing files from a Linux or macOS computer. There are many of these out there, but this one does things in an interesting way, notably shares are designed to be timebound (lasting no more than an hour or two, but they can last longer if need be) and the daemon does not need to copy the file to some sort of spool directory (but it can!). It's designed to be quick...and should be self explanatory from this "screenshot":

```bash
$ fling share ubuntu-26.04-desktop-amd64.iso
https://fileflinger.example.com/d/mango-24-voter-spied
Name: ubuntu-26.04-desktop-amd64.iso    Size: 6.1 GiB   Expires: in 59m
```

This command returns instantly and requires no copying. Additionally, the daemon does **not** need any sort of read permissions to the files it is sharing, only the CLI needs that access.

# Installation and Setup

Fileflinger is distributed as an RPM, a DEB file, an SPK, or a single executable. The packages will create a `fileflinger` user that the daemon/webserver runs at (default port 6446) and a `fileflinger` group. Users must be in the `fileflinger` group in order to add files. This protection is accomplished by making the Unix socket that the CLI uses to communicate with the daemon writable only by members of that group.

Installation on Ubuntu looks like this:

```bash
$ sudo dpkg -i fileflinger_<version>_linux_amd64.deb
```

After install, you should do a couple things:

1. Set the `base_url` in `/etc/fileflinger/fileflinger.toml` to the server you are hosting this from (this is only used in URL construction, so the CLI can print the full URL of the file).
2. (Optional) Put what header we should read for client IP information (this is used to restrict download counts, so it's important!)
3. Add yourself to the `fileflinger` group (`sudo usermod -aG fileflinger $USER`, you will likely need to log out and then log back in to take effect)
4. You'll probably want to put this behind some sort of reverse proxy or something

Once you've done all that, you can start `fileflinger`:

```bash
$ sudo systemctl start fileflinger
```

# Sharing a File

In order to share a file, you need to add a "root". Any file shared must be in a root, which is a path on your filesystem that is opted-in to sharing. This does *not* automatically share any file in that root, it just allows you to. This is so you can't accidentally type `fling share /etc/passwd` or something stupid like that.

You can add a root by running

```bash
$ fling roots add /path/to/your/root
```

Roots can be listed or deleted by other subcommands under `fling roots`

To share a file, just run `fling share /path/to/file.txt` and you should have a URL to give to a friend. 

Shares can be listed and revoke with `fling list` and `fling revoke` (`fling revoke -a` to revoke all shares).

By default, shares will last for one hour. This can be overriden with the `--ttl` flag (e.g. `--ttl 48h` for 48-hours)

Help that shows all the flags and options can be shown with `fling --help` and help on a certain command can be done with `fling share --help`

# Architecture

There are three major components to `fileflinger`: 1) the CLI, 2) a daemon, and 3) a webserver. In the default configuration, the daemon and the webserver run in the same executable, but this is not required (TODO: document how to do this). You may wish to do this if you want to run the webserver in an isolated environment such as a Docker container. 

The CLI communicates with the daemon using gRPC over a Unix socket. If the server is a separate process, it also uses the gRPC/socket to communicate. There is an additional Unix socket for passing Unix file descriptors (which is how the daemon doesn't actually need read access to the files it shares).

So when you run `fling share /path/to/some/file.txt` here's what happens

1) The CLI opens the file for reading (proving you have read access to the file).
2) The CLI contacts the daemon and says "hey, I've got a file I want to share!"
3) The Daemon responds "Great! Here's a share code!"
4) The CLI uses that share code and an additional Unix socket to send the opened file descriptor to the daemon.
5) The CLI then tells the daemon "hey! I've sent that file descriptor"
6) The daemon, reads the file descriptor, resolves the true path, makes sure it's sharable (i.e. within a root) and then returns the final URL for the CLI to display to the user.

