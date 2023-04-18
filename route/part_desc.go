package route

import (
	"errors"
	"github.com/oceanbase/obkv-table-client-go/log"
	"github.com/oceanbase/obkv-table-client-go/protocol"
	"github.com/oceanbase/obkv-table-client-go/table"
	"strconv"
	"strings"
)

const (
	ObPartIdBitNum  = 28
	ObPartIdShift   = 32
	ObMask          = (1 << ObPartIdBitNum) | 1<<(ObPartIdBitNum+ObPartIdShift)
	ObSubPartIdMask = 0xffffffff & 0xfffffff
)

type ObColumnIndexesPair struct {
	column  *protocol.ObColumn
	indexes []int
}

func NewObColumnIndexesPair(column *protocol.ObColumn, indexes []int) *ObColumnIndexesPair {
	return &ObColumnIndexesPair{column, indexes}
}

func (p *ObColumnIndexesPair) String() string {
	columnStr := "nil"
	if p.column != nil {
		columnStr = p.column.String()
	}
	var indexesStr string
	indexesStr = indexesStr + "["
	for i := 0; i < len(p.indexes); i++ {
		if i > 0 {
			indexesStr += ", "
		}
		indexesStr += strconv.Itoa(p.indexes[i])
	}
	indexesStr += "]"
	return "ObColumnIndexesPair{" +
		"column:" + columnStr + ", " +
		"indexes:" + indexesStr +
		"}"
}

type ObPartDescCommon struct {
	PartFuncType ObPartFuncType
	PartExpr     string
	// orderedPartColumnNames Represents all partitioned column names
	// eg:
	//    partition by range(c1, c2)
	//    orderedPartColumnNames = ["c1", "c2"]
	OrderedPartColumnNames              []string
	OrderedPartRefColumnRowKeyRelations []*ObColumnIndexesPair
	PartColumns                         []*protocol.ObColumn
	RowKeyElement                       *table.ObRowkeyElement
}

func (c *ObPartDescCommon) setCommRowKeyElement(rowKeyElement *table.ObRowkeyElement) {
	c.RowKeyElement = rowKeyElement
	if len(c.OrderedPartColumnNames) != 0 && len(c.PartColumns) != 0 {
		relations := make([]*ObColumnIndexesPair, 0, len(c.OrderedPartColumnNames))
		for _, partOrderColumnName := range c.OrderedPartColumnNames {
			for _, col := range c.PartColumns {
				if strings.EqualFold(col.ColumnName(), partOrderColumnName) {
					partRefColumnRowKeyIndexes := make([]int, 0, len(col.RefColumnNames()))
					for _, refColumnName := range col.RefColumnNames() {
						for rowKeyElementName, index := range c.RowKeyElement.NameIdxMap() {
							if strings.EqualFold(rowKeyElementName, refColumnName) {
								partRefColumnRowKeyIndexes = append(partRefColumnRowKeyIndexes, index)
							}
						}
					}
					pair := NewObColumnIndexesPair(col, partRefColumnRowKeyIndexes)
					relations = append(relations, pair)
				}
			}
		}
		c.OrderedPartRefColumnRowKeyRelations = relations
	}
}

func (c *ObPartDescCommon) CommString() string {
	// orderedPartRefColumnRowKeyRelations to string
	var relationsStr string
	relationsStr = relationsStr + "["
	for i := 0; i < len(c.OrderedPartRefColumnRowKeyRelations); i++ {
		if i > 0 {
			relationsStr += ", "
		}
		relationsStr += c.OrderedPartRefColumnRowKeyRelations[i].String()
	}
	relationsStr += "]"

	// partColumns to string
	var partColumnsStr string
	partColumnsStr = partColumnsStr + "["
	for i := 0; i < len(c.PartColumns); i++ {
		if i > 0 {
			partColumnsStr += ", "
		}
		partColumnsStr += c.PartColumns[i].String()
	}
	partColumnsStr += "]"

	// rowKeyElement to string
	rowKeyElementStr := "nil"
	if c.RowKeyElement != nil {
		rowKeyElementStr = c.RowKeyElement.String()
	}

	return "ObPartDescCommon{" +
		"partFuncType:" + c.PartFuncType.String() + ", " +
		"partExpr:" + c.PartExpr + ", " +
		"orderedPartColumnNames:" + strings.Join(c.OrderedPartColumnNames, ",") + ", " +
		"orderedPartRefColumnRowKeyRelations:" + relationsStr + ", " +
		"partColumns:" + partColumnsStr + ", " +
		"rowKeyElement:" + rowKeyElementStr +
		"}"
}

type ObPartDesc interface {
	String() string
	partFuncType() ObPartFuncType
	orderedPartColumnNames() []string
	setOrderedPartColumnNames(partExpr string)
	orderedPartRefColumnRowKeyRelations() []*ObColumnIndexesPair
	rowKeyElement() *table.ObRowkeyElement
	setRowKeyElement(rowKeyElement *table.ObRowkeyElement)
	setPartColumns(partColumns []*protocol.ObColumn)
	GetPartId(rowkey []interface{}) (int64, error)
}

func evalPartKeyValues(desc ObPartDesc, rowkey []interface{}) ([]interface{}, error) {
	if desc == nil {
		log.Warn("part desc is nil")
		return nil, errors.New("part desc is nil")
	}
	if len(rowkey) < len(desc.rowKeyElement().NameIdxMap()) {
		log.Warn("rowkey count not match",
			log.Int("rowkey len", len(rowkey)),
			log.Int("rowKeyElement len", len(desc.rowKeyElement().NameIdxMap())))
		return nil, errors.New("rowkey count not match")
	}
	partRefColumnSize := len(desc.orderedPartRefColumnRowKeyRelations())
	evalValues := make([]interface{}, 0, partRefColumnSize)
	for i := 0; i < partRefColumnSize; i++ {
		relation := desc.orderedPartRefColumnRowKeyRelations()[i]
		evalParams := make([]interface{}, len(relation.indexes))
		for i := 0; i < len(relation.indexes); i++ {
			evalParams[i] = rowkey[relation.indexes[i]]
		}
		val, err := relation.column.EvalValue(evalParams...)
		if err != nil {
			log.Warn("fail to eval column value", log.String("relations", relation.String()))
			return nil, err
		}
		evalValues = append(evalValues, val)
	}
	return evalValues, nil
}

type ObRangePartDesc struct {
	ObPartDescCommon
	partSpace                 int
	partNum                   int
	orderedCompareColumns     []*protocol.ObColumn
	orderedCompareColumnTypes []protocol.ObObjType
	//todo: List<ObComparableKV<ObPartitionKey, Long>> bounds
}

func newObRangePartDesc() *ObRangePartDesc {
	return &ObRangePartDesc{}
}

func (d *ObRangePartDesc) partFuncType() ObPartFuncType {
	return d.PartFuncType
}

func (d *ObRangePartDesc) orderedPartColumnNames() []string {
	return d.OrderedPartColumnNames
}

func (d *ObRangePartDesc) setOrderedPartColumnNames(partExpr string) {
	// eg:"c1, c2", need to remove ' '
	str := strings.ReplaceAll(partExpr, " ", "")
	d.OrderedPartColumnNames = strings.Split(str, ",")
}

func (d *ObRangePartDesc) orderedPartRefColumnRowKeyRelations() []*ObColumnIndexesPair {
	return d.OrderedPartRefColumnRowKeyRelations
}
func (d *ObRangePartDesc) rowKeyElement() *table.ObRowkeyElement {
	return d.RowKeyElement
}

func (d *ObRangePartDesc) setRowKeyElement(rowKeyElement *table.ObRowkeyElement) {
	d.setCommRowKeyElement(rowKeyElement)
}

func (d *ObRangePartDesc) setPartColumns(partColumns []*protocol.ObColumn) {
	d.PartColumns = partColumns
}

func (d *ObRangePartDesc) GetPartId(rowkey []interface{}) (int64, error) {
	// todo: impl
	return -1, errors.New("not implement")
}

//func (d *ObRangePartDesc) setOrderedCompareColumns(orderedPartColumn []protocol.ObColumn) {
//	d.orderedCompareColumns = orderedPartColumn
//}

func (d *ObRangePartDesc) String() string {
	// orderedCompareColumns to string
	var orderedCompareColumnsStr string
	orderedCompareColumnsStr = orderedCompareColumnsStr + "["
	for i := 0; i < len(d.orderedCompareColumns); i++ {
		if i > 0 {
			orderedCompareColumnsStr += ", "
		}
		orderedCompareColumnsStr += d.orderedCompareColumns[i].String()
	}
	orderedCompareColumnsStr += "]"

	// orderedCompareColumnTypes to string
	var orderedCompareColumnTypesStr string
	orderedCompareColumnTypesStr = orderedCompareColumnTypesStr + "["
	for i := 0; i < len(d.orderedCompareColumns); i++ {
		if i > 0 {
			orderedCompareColumnTypesStr += ", "
		}
		orderedCompareColumnTypesStr += d.orderedCompareColumnTypes[i].String()
	}
	orderedCompareColumnTypesStr += "]"

	return "ObRangePartDesc{" +
		"comm:" + d.CommString() + ", " +
		"partSpace:" + strconv.Itoa(d.partSpace) + ", " +
		"partNum:" + strconv.Itoa(d.partNum) + ", " +
		"orderedCompareColumns:" + orderedCompareColumnsStr + ", " +
		"orderedCompareColumnTypes:" + orderedCompareColumnTypesStr +
		"}"
}

type ObHashPartDesc struct {
	ObPartDescCommon
	completeWorks []int64
	partSpace     int
	partNum       int
	partNameIdMap map[string]int64
}

func newObHashPartDesc() *ObHashPartDesc {
	return &ObHashPartDesc{}
}

func (d *ObHashPartDesc) partFuncType() ObPartFuncType {
	return d.PartFuncType
}

func (d *ObHashPartDesc) orderedPartColumnNames() []string {
	return d.OrderedPartColumnNames
}

func (d *ObHashPartDesc) setOrderedPartColumnNames(partExpr string) {
	// eg:"c1, c2", need to remove ' '
	str := strings.ReplaceAll(partExpr, " ", "")
	d.OrderedPartColumnNames = strings.Split(str, ",")
}

func (d *ObHashPartDesc) orderedPartRefColumnRowKeyRelations() []*ObColumnIndexesPair {
	return d.OrderedPartRefColumnRowKeyRelations
}
func (d *ObHashPartDesc) rowKeyElement() *table.ObRowkeyElement {
	return d.RowKeyElement
}

func (d *ObHashPartDesc) setRowKeyElement(rowKeyElement *table.ObRowkeyElement) {
	d.setCommRowKeyElement(rowKeyElement)
}

func (d *ObHashPartDesc) setPartColumns(partColumns []*protocol.ObColumn) {
	d.PartColumns = partColumns
}

func (d *ObHashPartDesc) GetPartId(rowkey []interface{}) (int64, error) {
	if len(rowkey) == 0 {
		log.Warn("rowkey size is 0")
		return -1, errors.New("rowkeys size is 0")
	}
	evalValues, err := evalPartKeyValues(d, rowkey)
	if err != nil {
		log.Warn("failed to eval part key values", log.String("part desc", d.String()))
		return -1, err
	}
	longValue, err := protocol.ParseToLong(evalValues[0]) // hash part has one param at most
	if err != nil {
		log.Warn("failed to parse to long", log.String("part desc", d.String()))
		return -1, err
	}
	if v, ok := longValue.(int64); !ok {
		log.Warn("failed to convert to long")
		return -1, errors.New("failed to convert to long")
	} else {
		return d.innerHash(v), nil
	}
}

func (d *ObHashPartDesc) innerHash(hashVal int64) int64 {
	// abs(hashVal)
	if hashVal < 0 {
		hashVal = -hashVal
	}
	return (int64(d.partSpace) << ObPartIdBitNum) | (hashVal % int64(d.partNum))
}

func (d *ObHashPartDesc) String() string {
	// completeWorks to string
	var completeWorksStr string
	completeWorksStr = completeWorksStr + "["
	for i := 0; i < len(d.completeWorks); i++ {
		if i > 0 {
			completeWorksStr += ", "
		}
		completeWorksStr += strconv.Itoa(int(d.completeWorks[i]))
	}
	completeWorksStr += "]"

	// partNameIdMap to string
	var partNameIdMapStr string
	partNameIdMapStr = partNameIdMapStr + "{"
	var i = 0
	for k, v := range d.partNameIdMap {
		if i > 0 {
			partNameIdMapStr += ", "
		}
		i++
		partNameIdMapStr += "m[" + k + "]=" + strconv.Itoa(int(v))
	}
	partNameIdMapStr += "}"

	return "ObHashPartDesc{" +
		"comm:" + d.CommString() + ", " +
		"completeWorks:" + completeWorksStr + ", " +
		"partSpace:" + strconv.Itoa(d.partSpace) + ", " +
		"partNum:" + strconv.Itoa(d.partNum) + ", " +
		"partNameIdMap:" + partNameIdMapStr +
		"}"
}

type ObKeyPartDesc struct {
	ObPartDescCommon
	partSpace     int
	partNum       int
	partNameIdMap map[string]int64
}

func newObKeyPartDesc() *ObKeyPartDesc {
	return &ObKeyPartDesc{}
}

func (d *ObKeyPartDesc) partFuncType() ObPartFuncType {
	return d.PartFuncType
}

func (d *ObKeyPartDesc) orderedPartColumnNames() []string {
	return d.OrderedPartColumnNames
}

func (d *ObKeyPartDesc) setOrderedPartColumnNames(partExpr string) {
	// eg:"c1, c2", need to remove ' '
	str := strings.ReplaceAll(partExpr, " ", "")
	d.OrderedPartColumnNames = strings.Split(str, ",")
}

func (d *ObKeyPartDesc) orderedPartRefColumnRowKeyRelations() []*ObColumnIndexesPair {
	return d.OrderedPartRefColumnRowKeyRelations
}

func (d *ObKeyPartDesc) rowKeyElement() *table.ObRowkeyElement {
	return d.RowKeyElement
}

func (d *ObKeyPartDesc) setRowKeyElement(rowKeyElement *table.ObRowkeyElement) {
	d.setCommRowKeyElement(rowKeyElement)
}

func (d *ObKeyPartDesc) setPartColumns(partColumns []*protocol.ObColumn) {
	d.PartColumns = partColumns
}

func (d *ObKeyPartDesc) GetPartId(rowkey []interface{}) (int64, error) {
	// todo: impl
	return -1, errors.New("not implement")
}

func (d *ObKeyPartDesc) String() string {
	// partNameIdMap to string
	var partNameIdMapStr string
	partNameIdMapStr = partNameIdMapStr + "{"
	var i = 0
	for k, v := range d.partNameIdMap {
		if i > 0 {
			partNameIdMapStr += ", "
		}
		i++
		partNameIdMapStr += "m[" + k + "]=" + strconv.Itoa(int(v))
	}
	partNameIdMapStr += "}"
	return "ObKeyPartDesc{" +
		"comm:" + d.CommString() + ", " +
		"partSpace:" + strconv.Itoa(d.partSpace) + ", " +
		"partNum:" + strconv.Itoa(d.partNum) + ", " +
		"partNameIdMap:" + partNameIdMapStr +
		"}"
}

const (
	PartLevelUnknown = "partLevelUnknown"
	PartLevelZero    = "partLevelZero"
	PartLevelOne     = "partLevelOne"
	PartLevelTwo     = "partLevelTwo"
)

const (
	PartLevelUnknownIndex = -1
	PartLevelZeroIndex    = 0
	PartLevelOneIndex     = 1
	PartLevelTwoIndex     = 2
)

type ObPartitionLevel struct {
	name  string
	index int
}

func (l ObPartitionLevel) Index() int {
	return l.index
}

func newObPartitionLevel(index int) ObPartitionLevel {
	switch index {
	case PartLevelZeroIndex:
		return ObPartitionLevel{PartLevelZero, PartLevelZeroIndex}
	case PartLevelOneIndex:
		return ObPartitionLevel{PartLevelOne, PartLevelOneIndex}
	case PartLevelTwoIndex:
		return ObPartitionLevel{PartLevelTwo, PartLevelTwoIndex}
	default:
		return ObPartitionLevel{PartLevelUnknown, PartLevelUnknownIndex}
	}
}

func (l ObPartitionLevel) String() string {
	return "ObPartitionLevel{" +
		"name:" + l.name + ", " +
		"index:" + strconv.Itoa(l.index) +
		"}"
}

const (
	partFuncTypeUnknownIndex  = -1
	partFuncTypeHashIndex     = 0
	partFuncTypeKeyIndex      = 1
	partFuncTypeKeyImplIndex  = 2
	partFuncTypeRangeIndex    = 3
	partFuncTypeRangeColIndex = 4
	partFuncTypeListIndex     = 5
	partFuncTypeKeyV2Index    = 6
	partFuncTypeListColIndex  = 7
	partFuncTypeHashV2Index   = 8
	partFuncTypeKeyV3Index    = 9
)

const (
	partFuncTypeUnknown  = "UNKNOWN"
	partFuncTypeHash     = "HASH"
	partFuncTypeKey      = "KEY"
	partFuncTypeKeyImpl  = "KEY_IMPLICIT"
	partFuncTypeRange    = "RANGE"
	partFuncTypeRangeCol = "RANGE_COLUMNS"
	partFuncTypeList     = "LIST"
	partFuncTypeKeyV2    = "KEY_V2"
	partFuncTypeListCol  = "LIST_COLUMNS"
	partFuncTypeHashV2   = "HASH_V2"
	partFuncTypeKeyV3    = "KEY_V3"
)

type ObPartFuncType struct {
	name  string
	index int
}

func (t ObPartFuncType) String() string {
	return "ObPartFuncType{" +
		"name:" + t.name + ", " +
		"index:" + strconv.Itoa(t.index) +
		"}"
}

func (t ObPartFuncType) isRangePart() bool {
	return t.index == partFuncTypeRangeIndex || t.index == partFuncTypeRangeColIndex
}

func (t ObPartFuncType) isKeyPart() bool {
	return t.index == partFuncTypeKeyImplIndex ||
		t.index == partFuncTypeKeyV2Index ||
		t.index == partFuncTypeKeyV3Index
}

func (t ObPartFuncType) isHashPart() bool {
	return t.index == partFuncTypeHashIndex || t.index == partFuncTypeHashV2Index
}

func (t ObPartFuncType) isListPart() bool {
	return t.index == partFuncTypeListIndex || t.index == partFuncTypeListColIndex
}

func newObPartFuncType(index int) ObPartFuncType {
	switch index {
	case partFuncTypeHashIndex:
		return ObPartFuncType{partFuncTypeHash, partFuncTypeHashIndex}
	case partFuncTypeKeyIndex:
		return ObPartFuncType{partFuncTypeKey, partFuncTypeKeyIndex}
	case partFuncTypeKeyImplIndex:
		return ObPartFuncType{partFuncTypeKeyImpl, partFuncTypeKeyImplIndex}
	case partFuncTypeRangeIndex:
		return ObPartFuncType{partFuncTypeRange, partFuncTypeRangeIndex}
	case partFuncTypeRangeColIndex:
		return ObPartFuncType{partFuncTypeRangeCol, partFuncTypeRangeColIndex}
	case partFuncTypeListIndex:
		return ObPartFuncType{partFuncTypeList, partFuncTypeListIndex}
	case partFuncTypeKeyV2Index:
		return ObPartFuncType{partFuncTypeKeyV2, partFuncTypeKeyV2Index}
	case partFuncTypeListColIndex:
		return ObPartFuncType{partFuncTypeListCol, partFuncTypeListColIndex}
	case partFuncTypeHashV2Index:
		return ObPartFuncType{partFuncTypeHashV2, partFuncTypeHashV2Index}
	case partFuncTypeKeyV3Index:
		return ObPartFuncType{partFuncTypeKeyV3, partFuncTypeKeyV3Index}
	default:
		return ObPartFuncType{partFuncTypeUnknown, partFuncTypeUnknownIndex}
	}
}
