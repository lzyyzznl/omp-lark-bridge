package com.omp.mobile.data

import android.content.Context
import androidx.room.Dao
import androidx.room.Database
import androidx.room.Entity
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.PrimaryKey
import androidx.room.Query
import androidx.room.Room
import androidx.room.RoomDatabase
import kotlinx.coroutines.flow.Flow

@Entity(tableName = "nodes")
data class NodeEntity(
    @PrimaryKey val id: String,
    val name: String,
    val online: Boolean,
    val lastSeen: String,
    val agentCount: Int,
    val sessionCount: Int,
    val webUrl: String,
    val secretSet: Boolean,
    val loginCodeSet: Boolean,
)

@Dao
interface NodeDao {
    @Query("SELECT * FROM nodes ORDER BY name COLLATE NOCASE")
    fun observe(): Flow<List<NodeEntity>>

    @Query("SELECT * FROM nodes ORDER BY name COLLATE NOCASE")
    suspend fun all(): List<NodeEntity>

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun upsert(nodes: List<NodeEntity>)

    @Query("DELETE FROM nodes")
    suspend fun clear()
}

@Database(entities = [NodeEntity::class], version = 1, exportSchema = false)
abstract class AppDatabase : RoomDatabase() {
    abstract fun nodes(): NodeDao

    companion object {
        fun build(context: Context): AppDatabase =
            Room.databaseBuilder(context, AppDatabase::class.java, "omp.db").build()
    }
}
