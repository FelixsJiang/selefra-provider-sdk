package schema

import (
	"context"
	"errors"
	"fmt"
	"github.com/emirpasic/gods/maps/treemap"
)

// TableRuntime The runtime of the table, the relevant context during the runtime and so forth will be taken care of by this struct
type TableRuntime struct {
	myTable     *Table
	parentTable *Table

	// like database name
	Namespace string

	// The order in which columns are resolved, Because there may be a dependency order between columns,
	// the resolution order is obtained by topological sorting of the DAG
	ColumnExtractorSorted []*Column

	validator tableValidator

	// Column name to column mapping
	columnMap map[string]*Column
}

// Init Initializes the runtime of the table
func (x *TableRuntime) Init(ctx context.Context, clientMeta *ClientMeta, parentTable *Table, myTable *Table) *Diagnostics {

	x.myTable = myTable
	x.parentTable = parentTable

	diagnostics := NewDiagnostics()

	// Initialize yourself first
	diagnostics.AddDiagnostics(x.initMySelf(ctx, clientMeta))
	if diagnostics.HasError() {
		return diagnostics
	}

	// After you've initialized yourself, recursively initialize the child myTable if you have one
	for _, subTable := range myTable.SubTables {
		d := subTable.Runtime().Init(ctx, clientMeta, myTable, subTable)
		if diagnostics.AddDiagnostics(d).HasError() {
			return diagnostics
		}
	}

	return diagnostics
}

// Initialize yourself
func (x *TableRuntime) initMySelf(ctx context.Context, clientMeta *ClientMeta) *Diagnostics {

	diagnostics := NewDiagnostics()

	// Computes the parse order of the columns in the table
	if err := x.topologicalSortingColumnExtractorSorted(ctx, clientMeta); err != nil {
		return diagnostics.AddErrorMsg(err.Error())
	}

	// Initialize the mapping table
	x.columnMap = make(map[string]*Column, 0)
	for _, column := range x.myTable.Columns {
		x.columnMap[column.ColumnName] = column
	}

	return diagnostics
}

// ContainsColumnName Whether the table contains the given column
func (x *TableRuntime) ContainsColumnName(columnName string) bool {
	_, exists := x.columnMap[columnName]
	return exists
}

// Topologically sort dependencies between columns so that they can be resolved later
func (x *TableRuntime) topologicalSortingColumnExtractorSorted(ctx context.Context, clientMeta *ClientMeta) error {

	columnNameToColumnMap := make(map[string]*Column, 0)
	// Sort out a table whose columns are dependent. key is Column name, value is which columns depend on this column.
	columnDependencySetMap := treemap.NewWithStringComparator()
	// Parse which columns are dependent on
	getDependencyColumnNameSet := func(ctx context.Context, table *Table, column *Column) map[string]struct{} {
		dependencyColumnNameSet := make(map[string]struct{})
		// If the decimator is not set, then there is no dependency column, and no dependency column needs to be processed
		//if column.Extractor == nil || column.Extractor.DependencyColumnNames == nil {
		if column.Extractor == nil {
			return dependencyColumnNameSet
		}
		for _, columnName := range column.Extractor.DependencyColumnNames(ctx, clientMeta, x.parentTable, x.myTable, column) {
			dependencyColumnNameSet[columnName] = struct{}{}
		}
		return dependencyColumnNameSet
	}
	// Sorting out relationships that depend on being depended on
	for _, column := range x.myTable.Columns {
		currentColumnName := column.ColumnName

		// Make a column name to column struct quick query table
		columnNameToColumnMap[currentColumnName] = column

		// Which columns you depend on
		columnDependencySetMap.Put(currentColumnName, getDependencyColumnNameSet(ctx, x.myTable, column))
	}

	// Look for columns with degree 0
	findInDegreeZeroColumn := func() string {
		for _, columnName := range columnDependencySetMap.Keys() {
			value, _ := columnDependencySetMap.Get(columnName)
			if len(value.(map[string]struct{})) == 0 {
				return columnName.(string)
			}
		}
		return ""
	}
	// The result of the topological sort
	columnExtractorSorted := make([]*Column, 0)
	// Then we start topological sorting, looking for columns with input degree 0
	for !columnDependencySetMap.Empty() {
		// Look for the point where the degree of entry is zero
		columnName := findInDegreeZeroColumn()
		if columnName == "" {
			return errors.New(fmt.Sprintf("table %s topological sorting error: have circle!", x.myTable.TableName))
		}
		// Add to the result of topological sorting
		column := columnNameToColumnMap[columnName]
		if column == nil {
			return errors.New(fmt.Sprintf("table %s topological sorting error: i can not understand what happen!", x.myTable.TableName))
		}
		columnExtractorSorted = append(columnExtractorSorted, column)
		// Delete yourself from the picture
		columnDependencySetMap.Remove(columnName)

		columnDependencySetMap.Each(func(key any, value any) {
			dependencyColumnNameSet := value.(map[string]struct{})
			if _, exists := dependencyColumnNameSet[columnName]; exists {
				delete(dependencyColumnNameSet, columnName)
			}
		})

	}
	x.ColumnExtractorSorted = columnExtractorSorted
	return nil
}

// Validate The table is self-checked
func (x *TableRuntime) Validate(ctx context.Context, clientMeta *ClientMeta, parentTable *Table, table *Table) *Diagnostics {
	return x.validator.validate(ctx, clientMeta, parentTable, table)
}
