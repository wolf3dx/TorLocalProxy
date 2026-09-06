package com.vkandreevich.torlocalproxy;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Intent;
import android.os.Build;
import android.os.IBinder;

/**
 * Служба переднего плана. Своей работы не делает — её единственная задача
 * держать процесс приложения живым.
 *
 * Android свободно убивает фоновые приложения: под нехватку памяти или
 * просто по смахиванию из списка задач. Вместе с приложением умирает и
 * tor, а прокси перестаёт отвечать — причём пользователь узнаёт об этом
 * не сразу, а когда у него перестанет открываться сайт. Пока служба
 * висит в переднем плане с постоянным уведомлением, система процесс не
 * трогает.
 *
 * Служба работает в том же процессе, что и код на Go, поэтому отдельного
 * общения между ними не требуется: живёт процесс — живёт и tor.
 */
public class TorService extends Service {

    private static final String CHANNEL_ID = "torlocalproxy";
    private static final int NOTIFICATION_ID = 1;

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        startForeground(NOTIFICATION_ID, buildNotification());
        // START_STICKY: если систему всё же прижмёт и она нас снимет,
        // служба будет поднята заново.
        return START_STICKY;
    }

    private Notification buildNotification() {
        NotificationManager manager =
                (NotificationManager) getSystemService(NOTIFICATION_SERVICE);

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel channel = new NotificationChannel(
                    CHANNEL_ID, "TorLocalProxy",
                    NotificationManager.IMPORTANCE_LOW);
            channel.setDescription("Прокси работает в фоне");
            channel.setShowBadge(false);
            manager.createNotificationChannel(channel);
        }

        // По нажатию на уведомление открывается окно приложения.
        Intent open = new Intent();
        open.setClassName(this, "org.golang.app.GoNativeActivity");
        open.setFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        PendingIntent pending = PendingIntent.getActivity(
                this, 0, open,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);

        Notification.Builder builder = (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O)
                ? new Notification.Builder(this, CHANNEL_ID)
                : new Notification.Builder(this);

        return builder
                .setContentTitle("TorLocalProxy")
                .setContentText("Прокси работает")
                .setSmallIcon(android.R.drawable.ic_lock_lock)
                .setContentIntent(pending)
                .setOngoing(true)
                .build();
    }
}
