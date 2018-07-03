/*

Copyright (C) 2017-2018  Ettore Di Giacinto <mudler@gentoo.org>
Credits goes also to Gogs authors, some code portions and re-implemented design
are also coming from the Gogs project, which is using the go-macaron framework
and was really source of ispiration. Kudos to them!

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

*/

package nodesroute

import (
	"fmt"
	"sort"

	"github.com/MottainaiCI/mottainai-server/pkg/context"
	database "github.com/MottainaiCI/mottainai-server/pkg/db"
	agenttasks "github.com/MottainaiCI/mottainai-server/pkg/tasks"

	"github.com/MottainaiCI/mottainai-server/pkg/template"
)

func ShowAll(ctx *context.Context, db *database.Database) {
	//tasks := db.ListTasks()
	fmt.Println("ShowAll")
	nodes := db.AllNodes()
	//ctx.Data["TasksIDs"] = tasks
	ctx.Data["Nodes"] = nodes
	template.TemplatePreview(ctx, "nodes")
}

func Show(ctx *context.Context, db *database.Database) {
	id := ctx.ParamsInt(":id")

	node, err := db.GetNode(id)
	if err != nil {
		ctx.NotFound()
		return
	}
	ctx.Data["Node"] = node
	tasks, _ := db.FindDoc("Tasks", `[{"eq": "`+node.Hostname+node.NodeID+`", "in": ["queue"]}]`)
	var node_tasks = make([]agenttasks.Task, 0)
	for i, _ := range tasks {
		t, _ := db.GetTask(i)
		node_tasks = append(node_tasks, t)
	}
	sort.Slice(node_tasks[:], func(i, j int) bool {
		return node_tasks[i].CreatedTime > node_tasks[j].CreatedTime
	})

	ctx.Data["Tasks"] = node_tasks

	template.TemplatePreview(ctx, "nodes/show")
}
