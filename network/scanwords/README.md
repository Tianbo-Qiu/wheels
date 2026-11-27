# scanwords

The client sends a sentence in 4-byte pieces that cut words in half. The server reads it with `bufio.Scanner` and gets whole words back. [readloop](../readloop) showed that a stream has no boundaries. This wheel shows how a scanner puts boundaries back.

## Run

```sh
go run ./network/scanwords/server     # terminal 1
go run ./network/scanwords/client     # terminal 2: one piece every 500ms
```

Here's a run, with the client on the left and the server on the right:

```
wrote 4 bytes: "the "         got word: "the"
wrote 4 bytes: "bigg"
wrote 4 bytes: "er t"          got word: "bigger"
wrote 4 bytes: "he i"          got word: "the"
wrote 4 bytes: "nter"
wrote 4 bytes: "face"
wrote 4 bytes: ", th"          got word: "interface,"
...
wrote 1 bytes: "."
(client exits)                 got word: "abstraction."
                               done, got 8 words from 127.0.0.1:54937
```

Fourteen pieces went in and eight whole words came out. Each word shows up only once the space after it arrives.

You can also be the client yourself. Type words into `nc 127.0.0.1 9001` and press **Ctrl-D** to send a partial word without a newline. A newline counts as a space, so pressing Enter would end the word.

## How Scan works

`Scan` keeps its own buffer and repeats two steps until it has a token:

```go
scanner := bufio.NewScanner(conn)
scanner.Split(bufio.ScanWords) // token = run of non-space bytes (default is lines)

for scanner.Scan() {
	// "bigg" arrives: no space yet, so the split function asks for more,
	//                 and Scan keeps "bigg" and reads again
	// "er t" arrives: buffer is "bigger t"; "bigger" is complete → returned
	//                 "t" stays in the buffer for the next Scan
	log.Printf("got word: %q", scanner.Text())
}
// at EOF, a held word with no space after it ("abstraction.") is returned too
```

1. **Read** whatever the connection has into the buffer.
2. **Ask the split function** whether the buffer holds a complete token. If it does, return the token. If not, go back to step 1.

The split function decides what a token is. `ScanLines` (the default) splits on `\n`. `ScanWords` splits on any Unicode space, and `ScanRunes` splits into single characters. You can write your own too, and that's how a scanner handles other framings.

`Scan` returns `false` both at EOF and on a read error, so check `Err()` after the loop. A clean EOF leaves `Err()` as `nil`.

**A token can be at most 64 KiB by default.** A longer one makes `Scan` fail with `bufio.Scanner: token too long`. `scanner.Buffer` raises the limit.

## Writes are not reads

The client writes 4 bytes per `Write` call, but nothing guarantees they *arrive* as 4-byte pieces. TCP can merge them, and one `Read` on the server can pick up several. The pause between writes only makes separate arrivals likely.

The scanner hides its reads. To see them, wrap the connection in a reader that logs each one:

```go
// logReader wraps any io.Reader and logs what each Read returns.
type logReader struct{ r io.Reader }

func (l logReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	log.Printf("read %d bytes: %q", n, p[:n])
	return n, err
}

scanner := bufio.NewScanner(logReader{conn}) // NewScanner takes any io.Reader
```

With a pause between writes, each piece arrived alone:

```
read 4 bytes: "the "
read 4 bytes: "bigg"
read 4 bytes: "er t"
```

With no pause, the same 4-byte writes arrived merged:

```
read 4 bytes: "the "
read 12 bytes: "bigger the i"
read 16 bytes: "nterface, the we"
read 8 bytes: "aker the"
read 8 bytes: " abstrac"
read 5 bytes: "tion."
read 0 bytes: ""          ← EOF
```

Both runs produced the same eight words. Only the reads changed, and the scanner doesn't care how the bytes were grouped.

## Bytes, not runes

The client cuts the payload by **bytes**:

```go
piece := payload[i:min(i+4, len(payload))] // indexing a string counts bytes
```

A **rune** is one Unicode character (`int32`), and UTF-8 stores it in 1 to 4 bytes. This payload is all ASCII, so 1 byte = 1 rune. With other text, a cut can land inside a character:

```go
"café au lait"[0:4] // "caf\xc3": half of 'é'; its second byte is in the next piece
```

The scanner handles that too. It waits for the rest of the character the same way it waits for the rest of a word. To cut by characters instead, convert first with `[]rune(payload)`.
