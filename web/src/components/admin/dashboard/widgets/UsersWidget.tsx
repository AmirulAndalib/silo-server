import { Link, useNavigate } from "react-router";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useAdminUsers } from "@/hooks/queries/admin/users";
import { SectionError, UserSkeletonRows } from "../feedback";

export function UsersWidget() {
  const navigate = useNavigate();
  const usersQuery = useAdminUsers();
  const users = usersQuery.data ?? [];

  return (
    <Card className="h-full">
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-3">
        <CardTitle className="text-sm font-bold">Users</CardTitle>
        <Link
          to="/admin/users"
          className="text-muted-foreground hover:text-primary text-[11px] transition-colors"
        >
          Manage ›
        </Link>
      </CardHeader>
      <CardContent>
        {usersQuery.isLoading ? (
          <UserSkeletonRows />
        ) : usersQuery.error ? (
          <SectionError message="Failed to load users." />
        ) : users.length === 0 ? (
          <div className="text-muted-foreground py-4 text-center text-sm">No users.</div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>User</TableHead>
                <TableHead className="hidden sm:table-cell">Role</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.slice(0, 8).map((u) => (
                <TableRow
                  key={u.id}
                  className="hover:bg-accent/50 cursor-pointer"
                  onClick={() => navigate(`/admin/users/${u.id}`)}
                >
                  <TableCell>
                    <div className="flex items-center gap-2.5">
                      <div
                        className="text-primary-foreground flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-full text-[10px] font-bold"
                        style={{ background: `var(--primary)` }}
                      >
                        {u.username.charAt(0).toUpperCase()}
                      </div>
                      <div>
                        <div className="text-[13px] font-semibold">{u.username}</div>
                        <div className="text-muted-foreground hidden text-[10px] sm:block">
                          {u.email}
                        </div>
                      </div>
                    </div>
                  </TableCell>
                  <TableCell className="hidden sm:table-cell">
                    <Badge variant={u.role === "admin" ? "default" : "secondary"}>{u.role}</Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant={u.enabled ? "outline" : "destructive"}>
                      {u.enabled ? "Active" : "Disabled"}
                    </Badge>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
