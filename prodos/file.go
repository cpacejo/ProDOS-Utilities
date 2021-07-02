package prodos

import (
	"errors"
	"os"
	"strings"
	"time"
)

func LoadFile(file *os.File, path string) ([]byte, error) {
	fileEntry, err := getFileEntry(file, path)
	if err != nil {
		return nil, err
	}

	blockList, err := getDataBlocklist(file, fileEntry)
	if err != nil {
		return nil, err
	}

	buffer := make([]byte, fileEntry.EndOfFile)

	for i := 0; i < len(blockList); i++ {
		block := ReadBlock(file, blockList[i])
		for j := 0; j < 512 && i*512+j < fileEntry.EndOfFile; j++ {
			buffer[i*512+j] = block[j]
		}
	}

	return buffer, nil
}

func WriteFile(file *os.File, path string, fileType int, auxType int, buffer []byte) error {
	directory, fileName := GetDirectoryAndFileNameFromPath(path)

	existingFileEntry, _ := getFileEntry(file, path)
	if existingFileEntry.StorageType != StorageDeleted {
		DeleteFile(file, path)
	}

	// get list of blocks to write file to
	blockList := createBlockList(file, len(buffer))

	// seedling file
	if len(buffer) <= 0x200 {
		WriteBlock(file, blockList[0], buffer)
	}

	// sapling file needs index block
	if len(buffer) > 0x200 && len(buffer) <= 0x20000 {
		writeSaplingFile(file, buffer, blockList)
	}

	// TODO: add tree file
	if len(buffer) > 0x20000 {
		return errors.New("Files > 128KB not supported yet.")
	}

	updateVolumeBitmap(file, blockList)

	// add file entry to directory
	fileEntry, err := getFreeFileEntryInDirectory(file, directory)
	if err != nil {
		return err
	}
	fileEntry.FileName = fileName
	fileEntry.BlocksUsed = len(blockList)
	fileEntry.CreationTime = time.Now()
	fileEntry.ModifiedTime = time.Now()
	fileEntry.AuxType = auxType
	fileEntry.EndOfFile = len(buffer)
	fileEntry.FileType = fileType
	fileEntry.KeyPointer = blockList[0]
	fileEntry.Access = 0b11100011
	if len(blockList) == 1 {
		fileEntry.StorageType = StorageSeedling
	} else if len(blockList) <= 257 {
		fileEntry.StorageType = StorageSapling
	} else {
		fileEntry.StorageType = StorageTree
	}

	writeFileEntry(file, fileEntry)

	// increment file count
	directoryHeaderBlock := ReadBlock(file, fileEntry.HeaderPointer)
	directoryHeader := parseDirectoryHeader(directoryHeaderBlock, fileEntry.HeaderPointer)
	directoryHeader.ActiveFileCount++
	writeDirectoryHeader(file, directoryHeader)

	return nil
}

func updateVolumeBitmap(file *os.File, blockList []int) {
	volumeBitmap := ReadVolumeBitmap(file)
	for i := 0; i < len(blockList); i++ {
		markBlockInVolumeBitmap(volumeBitmap, blockList[i])
	}
	writeVolumeBitmap(file, volumeBitmap)
}

func writeSaplingFile(file *os.File, buffer []byte, blockList []int) {
	// write index block with pointers to data blocks
	indexBuffer := make([]byte, 512)
	for i := 0; i < 256; i++ {
		if i < len(blockList)-1 {
			indexBuffer[i] = byte(blockList[i+1] & 0x00FF)
			indexBuffer[i+256] = byte(blockList[i+1] >> 8)
		}
	}
	WriteBlock(file, blockList[0], indexBuffer)

	// write all data blocks
	blockBuffer := make([]byte, 512)
	blockPointer := 0
	blockIndexNumber := 1
	for i := 0; i < len(buffer); i++ {
		blockBuffer[blockPointer] = buffer[i]
		if blockPointer == 511 {
			WriteBlock(file, blockList[blockIndexNumber], blockBuffer)
			blockPointer = 0
			blockIndexNumber++
		} else if i == len(buffer)-1 {
			for j := blockPointer; j < 512; j++ {
				blockBuffer[j] = 0
			}
			WriteBlock(file, blockList[blockIndexNumber], blockBuffer)
		} else {
			blockPointer++
		}
	}
}

func DeleteFile(file *os.File, path string) error {
	fileEntry, err := getFileEntry(file, path)
	if err != nil {
		return errors.New("File not found")
	}
	if fileEntry.StorageType == StorageDeleted {
		return errors.New("File already deleted")
	}

	// free the blocks
	blocks, err := getBlocklist(file, fileEntry)
	if err != nil {
		return err
	}
	volumeBitmap := ReadVolumeBitmap(file)
	for i := 0; i < len(blocks); i++ {
		freeBlockInVolumeBitmap(volumeBitmap, blocks[i])
	}
	writeVolumeBitmap(file, volumeBitmap)

	// decrement the directory entry count
	directoryBlock := ReadBlock(file, fileEntry.HeaderPointer)
	directoryHeader := parseDirectoryHeader(directoryBlock, fileEntry.HeaderPointer)

	directoryHeader.ActiveFileCount--
	writeDirectoryHeader(file, directoryHeader)

	// zero out directory entry
	fileEntry.StorageType = 0
	fileEntry.FileName = ""
	writeFileEntry(file, fileEntry)

	return nil
}

func GetDirectoryAndFileNameFromPath(path string) (string, string) {
	path = strings.ToUpper(path)
	paths := strings.Split(path, "/")

	var directoryBuilder strings.Builder

	for i := 1; i < len(paths)-1; i++ {
		directoryBuilder.WriteString("/")
		directoryBuilder.WriteString(paths[i])
	}

	directory := directoryBuilder.String()
	fileName := paths[len(paths)-1]

	return directory, fileName
}

// Returns all blocks, including index blocks
func getBlocklist(file *os.File, fileEntry FileEntry) ([]int, error) {
	blocks := make([]int, fileEntry.BlocksUsed)

	switch fileEntry.StorageType {
	case StorageSeedling:
		blocks[0] = fileEntry.KeyPointer
		return blocks, nil
	case StorageSapling:
		index := ReadBlock(file, fileEntry.KeyPointer)
		blocks[0] = fileEntry.KeyPointer
		for i := 0; i < fileEntry.BlocksUsed-1; i++ {
			blocks[i+1] = int(index[i]) + int(index[i+256])*256
		}
		return blocks, nil
	case StorageTree:
		masterIndex := ReadBlock(file, fileEntry.KeyPointer)
		blocks[0] = fileEntry.KeyPointer
		for i := 0; i < 128; i++ {
			index := ReadBlock(file, int(masterIndex[i])+int(masterIndex[i+256])*256)
			for j := 0; j < 256 && i*256+j < fileEntry.BlocksUsed; j++ {
				if (int(index[j]) + int(index[j+256])*256) == 0 {
					return blocks, nil
				}
				blocks[i*256+j] = int(index[j]) + int(index[j+256])*256
			}
		}
	}

	return nil, errors.New("Unsupported file storage type")
}

func getDataBlocklist(file *os.File, fileEntry FileEntry) ([]int, error) {
	switch fileEntry.StorageType {
	case StorageSeedling:
		blocks := make([]int, 1)
		blocks[0] = fileEntry.KeyPointer
		return blocks, nil
	case StorageSapling:
		blocks := make([]int, fileEntry.BlocksUsed-1)
		index := ReadBlock(file, fileEntry.KeyPointer)
		for i := 0; i < fileEntry.BlocksUsed-1; i++ {
			blocks[i] = int(index[i]) + int(index[i+256])*256
		}
		return blocks, nil
	}

	return nil, errors.New("Unsupported file storage type")
}

func createBlockList(file *os.File, fileSize int) []int {
	numberOfBlocks := fileSize / 512
	if fileSize%512 > 0 {
		numberOfBlocks++
	}
	if fileSize > 0x200 && fileSize <= 0x20000 {
		numberOfBlocks++ // add index block
	}
	if fileSize > 0x20000 {
		// add master index block
		numberOfBlocks++
		// add index blocks for each 128 blocks
		numberOfBlocks += numberOfBlocks / 128
		// add index block for any remaining blocks
		if numberOfBlocks%128 > 0 {
			numberOfBlocks++
		}
	}
	volumeBitmap := ReadVolumeBitmap(file)
	blockList := findFreeBlocks(volumeBitmap, numberOfBlocks)

	return blockList
}

func getFileEntry(file *os.File, path string) (FileEntry, error) {
	directory, fileName := GetDirectoryAndFileNameFromPath(path)
	_, _, fileEntries := ReadDirectory(file, directory)

	if fileEntries == nil || len(fileEntries) == 0 {
		return FileEntry{}, errors.New("File entry not found")
	}

	var fileEntry FileEntry

	for i := 0; i < len(fileEntries); i++ {
		if fileEntries[i].FileName == fileName {
			fileEntry = fileEntries[i]
		}
	}

	if fileEntry.StorageType == StorageDeleted {
		return FileEntry{}, errors.New("File not found")
	}

	return fileEntry, nil
}
