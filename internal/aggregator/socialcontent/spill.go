package socialcontent

import (
	"bufio"
	"container/heap"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

const spillChunkRecords = 20000

// pinSpool stores backfill pins as JSON lines on disk so that a complete
// historical scan never has to keep the whole lookback set in memory.
type pinSpool struct {
	dir string
}

func newPinSpool() (*pinSpool, error) {
	dir, err := os.MkdirTemp("", "metaso-social-backfill-*")
	if err != nil {
		return nil, fmt.Errorf("create backfill spool: %w", err)
	}
	return &pinSpool{dir: dir}, nil
}

func (s *pinSpool) Close() error {
	if s == nil || s.dir == "" {
		return nil
	}
	return os.RemoveAll(s.dir)
}

func (s *pinSpool) filePath(name string) string {
	return filepath.Join(s.dir, name)
}

func (s *pinSpool) appendPins(name string, pins []BackfillPin) error {
	if len(pins) == 0 {
		return nil
	}
	file, err := os.OpenFile(s.filePath(name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open backfill spool %s: %w", name, err)
	}
	defer file.Close()
	writer := bufio.NewWriterSize(file, 1<<20)
	encoder := json.NewEncoder(writer)
	for _, pin := range pins {
		if err := encoder.Encode(pin); err != nil {
			return fmt.Errorf("write backfill spool %s: %w", name, err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush backfill spool %s: %w", name, err)
	}
	return nil
}

// filterPins reads one spool file, keeps records accepted by keep, and writes
// them to a second spool file without loading the whole set into memory.
func (s *pinSpool) filterPins(src, dst string, keep func(BackfillPin) (bool, error)) error {
	input, err := os.Open(s.filePath(src))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open backfill spool %s: %w", src, err)
	}
	defer input.Close()

	output, err := os.OpenFile(s.filePath(dst), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create backfill spool %s: %w", dst, err)
	}
	defer output.Close()
	writer := bufio.NewWriterSize(output, 1<<20)
	encoder := json.NewEncoder(writer)

	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var pin BackfillPin
		if err := json.Unmarshal(scanner.Bytes(), &pin); err != nil {
			return fmt.Errorf("decode backfill spool %s: %w", src, err)
		}
		accepted, err := keep(pin)
		if err != nil {
			return err
		}
		if accepted {
			if err := encoder.Encode(pin); err != nil {
				return fmt.Errorf("write backfill spool %s: %w", dst, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read backfill spool %s: %w", src, err)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush backfill spool %s: %w", dst, err)
	}
	return nil
}

// sortAndReplay sorts one spool file by the deterministic replay key and
// streams every record through replay with bounded memory.
func (s *pinSpool) sortAndReplay(name string, replay func(*aggregator.PinInscription) error) error {
	input, err := os.Open(s.filePath(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open backfill spool %s: %w", name, err)
	}
	defer input.Close()

	chunkFiles, err := s.writeSortedChunks(name, input)
	if err != nil {
		return err
	}
	defer func() {
		for _, file := range chunkFiles {
			_ = os.Remove(file)
		}
	}()
	if len(chunkFiles) == 0 {
		return nil
	}
	if len(chunkFiles) == 1 {
		return s.replayFile(chunkFiles[0], replay)
	}
	return s.mergeAndReplay(chunkFiles, replay)
}

func (s *pinSpool) writeSortedChunks(name string, input io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var chunk []BackfillPin
	var files []string
	chunkIndex := 0
	for scanner.Scan() {
		var pin BackfillPin
		if err := json.Unmarshal(scanner.Bytes(), &pin); err != nil {
			return nil, fmt.Errorf("decode backfill spool %s: %w", name, err)
		}
		chunk = append(chunk, pin)
		if len(chunk) >= spillChunkRecords {
			path, err := s.writeChunk(name, chunkIndex, chunk)
			if err != nil {
				return nil, err
			}
			files = append(files, path)
			chunk = chunk[:0]
			chunkIndex++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read backfill spool %s: %w", name, err)
	}
	if len(chunk) > 0 {
		path, err := s.writeChunk(name, chunkIndex, chunk)
		if err != nil {
			return nil, err
		}
		files = append(files, path)
	}
	return files, nil
}

func (s *pinSpool) writeChunk(name string, index int, pins []BackfillPin) (string, error) {
	path := s.filePath(fmt.Sprintf("chunk-%s-%05d.jsonl", name, index))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", fmt.Errorf("create backfill chunk %s: %w", path, err)
	}
	defer file.Close()
	sort.SliceStable(pins, func(i, j int) bool {
		return replayPinLess(pins[i], pins[j])
	})
	writer := bufio.NewWriterSize(file, 1<<20)
	encoder := json.NewEncoder(writer)
	for _, pin := range pins {
		if err := encoder.Encode(pin); err != nil {
			return "", fmt.Errorf("write backfill chunk %s: %w", path, err)
		}
	}
	if err := writer.Flush(); err != nil {
		return "", fmt.Errorf("flush backfill chunk %s: %w", path, err)
	}
	return path, nil
}

func (s *pinSpool) replayFile(path string, replay func(*aggregator.PinInscription) error) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open backfill chunk %s: %w", path, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var pin BackfillPin
		if err := json.Unmarshal(scanner.Bytes(), &pin); err != nil {
			return fmt.Errorf("decode backfill chunk %s: %w", path, err)
		}
		if err := replay(pin.toAggregatorPin()); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read backfill chunk %s: %w", path, err)
	}
	return nil
}

func (s *pinSpool) mergeAndReplay(paths []string, replay func(*aggregator.PinInscription) error) error {
	readers := make([]*bufio.Reader, len(paths))
	files := make([]*os.File, len(paths))
	for index, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open backfill chunk %s: %w", path, err)
		}
		files[index] = file
		readers[index] = bufio.NewReaderSize(file, 1<<20)
	}
	defer func() {
		for _, file := range files {
			if file != nil {
				_ = file.Close()
			}
		}
	}()

	h := &mergeHeap{}
	for index, reader := range readers {
		pin, err := readMergePin(reader)
		if err == io.EOF {
			continue
		}
		if err != nil {
			return err
		}
		heap.Push(h, mergeItem{pin: pin, reader: index})
	}
	for h.Len() > 0 {
		item := heap.Pop(h).(mergeItem)
		if err := replay(item.pin.toAggregatorPin()); err != nil {
			return err
		}
		pin, err := readMergePin(readers[item.reader])
		if err == io.EOF {
			continue
		}
		if err != nil {
			return err
		}
		heap.Push(h, mergeItem{pin: pin, reader: item.reader})
	}
	return nil
}

func readMergePin(reader *bufio.Reader) (BackfillPin, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return BackfillPin{}, err
	}
	var pin BackfillPin
	if err := json.Unmarshal(line, &pin); err != nil {
		return BackfillPin{}, fmt.Errorf("decode backfill merge record: %w", err)
	}
	return pin, nil
}

func replayPinLess(left, right BackfillPin) bool {
	if left.Timestamp != right.Timestamp {
		return left.Timestamp < right.Timestamp
	}
	if left.GenesisHeight != right.GenesisHeight {
		return left.GenesisHeight < right.GenesisHeight
	}
	return left.ID < right.ID
}

type mergeItem struct {
	pin    BackfillPin
	reader int
}

type mergeHeap []mergeItem

func (h mergeHeap) Len() int { return len(h) }

func (h mergeHeap) Less(i, j int) bool {
	return replayPinLess(h[i].pin, h[j].pin)
}

func (h mergeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *mergeHeap) Push(value any) { *h = append(*h, value.(mergeItem)) }

func (h *mergeHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}
