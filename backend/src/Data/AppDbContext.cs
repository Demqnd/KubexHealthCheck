using KubexHealthCheck.Models;
using Microsoft.EntityFrameworkCore;

namespace KubexHealthCheck.Data;

public class AppDbContext(DbContextOptions<AppDbContext> options) : DbContext(options)
{
    public DbSet<WebhookRoutine> WebhookRoutines => Set<WebhookRoutine>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        base.OnModelCreating(modelBuilder);

        modelBuilder.Entity<WebhookRoutine>(entity =>
        {
            entity.ToTable("webhook_routines");
            entity.HasKey(w => w.Id);
            entity.Property(w => w.Url).IsRequired().HasMaxLength(2048);
            entity.Property(w => w.UpdatedAtUtc).IsRequired();
        });
    }
}
