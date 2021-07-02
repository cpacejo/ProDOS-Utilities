package prodos

import (
	"fmt"
	"os"
	"testing"
)

func TestCreateVolume(t *testing.T) {
	var tests = []struct {
		blocks         int
		wantVolumeName string
		wantFreeBlocks int
	}{
		{65535, "MAX", 65513},
		{65500, "ALMOST.MAX", 65478},
		{280, "FLOPPY", 273},
	}

	for _, tt := range tests {
		testname := fmt.Sprintf("%d", tt.blocks)
		t.Run(testname, func(t *testing.T) {
			fileName := os.TempDir() + "test-volume.hdv"
			defer os.Remove(fileName)
			CreateVolume(fileName, tt.wantVolumeName, tt.blocks)

			file, err := os.Open(fileName)
			if err != nil {
				t.Error(err)
			}
			volumeHeader, _, fileEntries := ReadDirectory(file, "")
			if volumeHeader.VolumeName != tt.wantVolumeName {
				t.Errorf("got volume name %s, want %s", volumeHeader.VolumeName, tt.wantVolumeName)
			}
			if volumeHeader.TotalBlocks != tt.blocks {
				t.Errorf("got total blocks %d, want %d", volumeHeader.TotalBlocks, tt.blocks)
			}
			if len(fileEntries) > 0 {
				t.Errorf("got files %d, want 0", len(fileEntries))
			}

			volumeBitmap := ReadVolumeBitmap(file)
			freeBlockCount := getFreeBlockCount(volumeBitmap, tt.blocks)
			if freeBlockCount != tt.wantFreeBlocks {
				t.Errorf("got free blocks: %d, want %d", freeBlockCount, tt.wantFreeBlocks)
			}
		})
	}
}
